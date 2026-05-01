// Command audit-server is the slim HTTP host for the Figma plugin's
// /v1/audit/run endpoint. Designers run this on their laptop:
//
//	npm run audit:serve   # → cd services/ds-service && go run ./cmd/audit-server
//
// The plugin POSTs the active selection / page / file to localhost:7474,
// the server runs the audit core, and returns the structured AuditResult.
//
// Zero-auth, single-user, single-process. The full multi-tenant
// services/ds-service/cmd/server is a different beast and stays decoupled.
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

	mux := http.NewServeMux()
	mux.Handle("POST /v1/audit/run", audit.HandleAudit(cfg))
	mux.Handle("POST /v1/publish", audit.HandlePublish(cfg))
	// Plugin → ds-service forwarder. Exists because Figma plugin sandbox
	// (Origin: null iframe) cannot POST cross-origin to HTTPS Vercel
	// even with CORS-simple requests — the browser treats `null` origins
	// as opaque even when the response says ACAO: null. POST to localhost
	// works fine, so we route the projects.send pipeline through here.
	// Auth comes from body._auth (plugin's preflight-bypass shape) and we
	// forward as a Bearer header to ds-service.
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
