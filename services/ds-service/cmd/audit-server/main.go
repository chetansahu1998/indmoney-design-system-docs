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
	"context"
	"errors"
	"fmt"
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

	mux := http.NewServeMux()
	mux.Handle("POST /v1/audit/run", audit.HandleAudit(cfg))
	mux.Handle("POST /v1/publish", audit.HandlePublish(cfg))
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

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
