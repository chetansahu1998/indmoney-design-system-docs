// Command audit-server hosts the Figma plugin's /v1/audit/run, /v1/publish,
// and /v1/projects/export proxy endpoints.
//
// Originally a laptop-side single-user service. Now deployable to Fly: when
// REQUIRE_AUTH=1, every POST endpoint demands a valid ds-service JWT, supplied
// either as `Authorization: Bearer <token>` header OR as `body._auth` (the
// latter is the Figma plugin's preflight-bypass shape — Figma's `Origin: null`
// iframes can't send custom headers cross-origin without a preflight, so the
// plugin embeds the token in the JSON body instead). REQUIRE_AUTH=0 (default)
// keeps the historical zero-auth behaviour for `npm run audit:serve` on a dev
// laptop.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	dsauth "github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

// startedAt is captured at process boot — the plugin renders it as the
// server's "since" timestamp so a designer can tell at a glance whether
// the binary running is the one they just rebuilt.
var startedAt = time.Now().UTC()

// buildID captures the most recent commit revision baked into the binary.
// `go run` and unbuilt sources fall back to "(dev)". Real installs running
// `go build && ./audit-server` get a real hash.
func buildID() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(dev)"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 9 {
				return s.Value[:9]
			}
			return s.Value
		}
	}
	return "(dev)"
}

func main() {
	port := getenv("AUDIT_SERVER_PORT", "7474")

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	root := repo.Root()
	cfg := audit.HandlerConfig{RepoRoot: root}

	dsServiceURL := getenv("DS_SERVICE_URL", "http://localhost:8080")

	requireAuth := getenv("REQUIRE_AUTH", "0") == "1"
	var verifier *dsauth.SigningKey
	if requireAuth {
		var err error
		verifier, err = dsauth.LoadSigningKey(
			os.Getenv("JWT_SIGNING_KEY"),
			os.Getenv("JWT_PUBLIC_KEY"),
		)
		if err != nil {
			log.Error("REQUIRE_AUTH=1 but JWT keys unset/invalid", "err", err)
			os.Exit(1)
		}
		log.Info("auth enabled — every POST requires a valid ds-service JWT")
	} else {
		log.Info("auth disabled — set REQUIRE_AUTH=1 + JWT_SIGNING_KEY/JWT_PUBLIC_KEY for public deployments")
	}

	authMW := withAuth(verifier, requireAuth)

	mux := http.NewServeMux()
	mux.Handle("POST /v1/audit/run", authMW(audit.HandleAudit(cfg)))
	mux.Handle("POST /v1/publish", authMW(audit.HandlePublish(cfg)))
	// Plugin → ds-service forwarder. Exists because Figma plugin sandbox
	// (Origin: null iframe) cannot POST cross-origin to HTTPS Vercel
	// even with CORS-simple requests — the browser treats `null` origins
	// as opaque even when the response says ACAO: null. POST to localhost
	// works fine, so we route the projects.send pipeline through here.
	// Auth comes from body._auth (plugin's preflight-bypass shape) and we
	// forward as a Bearer header to ds-service. proxyExport keeps its own
	// _auth handling so the Bearer extraction + relay logic stays one place;
	// it is not wrapped by authMW.
	mux.Handle("POST /v1/projects/export", proxyExport(dsServiceURL))
	build := buildID()
	mux.HandleFunc("GET /__health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w,
			`{"ok":true,"repo":%q,"schema_version":%q,"build":%q,"started_at":%q,"endpoints":["/v1/audit/run","/v1/publish"]}`,
			root, audit.SchemaVersion, build, startedAt.Format(time.RFC3339),
		)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Info("audit-server listening",
		"addr", "http://localhost:"+port,
		"endpoint", "/v1/audit/run",
		"repo", root,
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}

// withCORS allows any origin (the Figma plugin sandbox sends `Origin: null`
// for some plugin requests; we don't authenticate so this is acceptable for
// a localhost-only server). Replace with a stricter allowlist if hosting publicly.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// proxyExport forwards the plugin's projects.send POST to ds-service.
// Reads the docs-site JWT from body._auth (the plugin embeds it there
// to dodge CORS preflight — see figma-plugin/code.ts for the rationale)
// and re-issues the request to ds-service with a proper Bearer header.
func proxyExport(dsServiceURL string) http.Handler {
	httpClient := &http.Client{Timeout: 90 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50MB cap
		if err != nil {
			httpJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "read_body", "detail": err.Error()})
			return
		}
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				httpJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_json", "detail": err.Error()})
				return
			}
		} else {
			body = map[string]any{}
		}
		token, _ := body["_auth"].(string)
		if token == "" {
			httpJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauth", "detail": "missing body._auth"})
			return
		}
		delete(body, "_auth")
		traceID, _ := body["trace_id"].(string)
		if traceID == "" {
			traceID = fmt.Sprintf("plugin-%d", time.Now().UnixNano())
		}

		fwdBody, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, dsServiceURL+"/v1/projects/export", bytes.NewReader(fwdBody))
		if err != nil {
			httpJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "build_req", "detail": err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Trace-ID", traceID)

		resp, err := httpClient.Do(req)
		if err != nil {
			httpJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "upstream_unreachable", "detail": err.Error(), "trace_id": traceID})
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		// Mirror upstream Content-Type if present so the plugin can JSON-parse.
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Set("X-Trace-ID", traceID)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
	})
}

// withAuth returns a middleware that requires a valid ds-service JWT on
// every wrapped request when `enabled` is true. When false it is a no-op.
//
// Token resolution order:
//  1. body._auth (Figma plugin's preflight-bypass shape; stripped from body
//     before the inner handler sees it so payload schemas stay clean)
//  2. Authorization: Bearer <token> header (regular HTTP clients)
//
// The body is buffered (capped at 50 MB to mirror proxyExport) so the inner
// handler still gets a fresh io.Reader; Content-Length is updated when _auth
// is removed so request validators downstream don't choke on the size delta.
func withAuth(verifier *dsauth.SigningKey, enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 50<<20))
			if err != nil {
				httpJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "read_body", "detail": err.Error()})
				return
			}
			_ = r.Body.Close()

			token := ""
			rewritten := raw
			if len(raw) > 0 {
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err == nil {
					if t, ok := body["_auth"].(string); ok && t != "" {
						token = t
						delete(body, "_auth")
						rewritten, _ = json.Marshal(body)
					}
				}
			}
			if token == "" {
				if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
					token = strings.TrimPrefix(h, "Bearer ")
				}
			}
			if token == "" {
				httpJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauth", "detail": "missing _auth body field or Authorization: Bearer header"})
				return
			}
			if _, err := verifier.VerifyAccessToken(token); err != nil {
				httpJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid_token", "detail": err.Error()})
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(rewritten))
			r.ContentLength = int64(len(rewritten))
			r.Header.Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
			next.ServeHTTP(w, r)
		})
	}
}

func httpJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
