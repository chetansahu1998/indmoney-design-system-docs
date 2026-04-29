// PNG route handler — U11.
//
// Serves project screen screenshots from the non-public storage path
// (services/ds-service/data/screens/<tenant_id>/<version_id>/<screen_id>@2x.png)
// through an authed Go route handler. Replaces the public/ static path
// approach the original plan flirted with — public Next.js static files
// have no auth gating and pre-launch product flows must not be world-readable.
//
// Security properties (per Phase 1 plan U11):
//   - JWT auth via existing requireAuth middleware (registered in cmd/server/main.go)
//   - tenant_id derived from JWT claims; never from URL/query/body
//   - cross-tenant lookups return 404 (NOT 403) — no existence oracle
//   - path traversal defense: filepath.Clean + strings.HasPrefix base check
//     on the resolved storage key, even though screen_id is server-generated UUID
//   - Cache-Control: private, max-age=300 (browsers cache, proxies don't)
//   - X-Content-Type-Options: nosniff (defense-in-depth against MIME sniffing)
//
// Phase 8 will swap the local-disk read for an S3 signed-URL redirect; the
// route shape (`GET /v1/projects/:slug/screens/:id/png`) doesn't change.

package projects

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// HandleScreenPNG returns the HTTP handler for `GET /v1/projects/:slug/screens/:id/png`.
// Caller wires this behind the existing requireAuth middleware in cmd/server.
func (s *Server) HandleScreenPNG() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims, _ := ctx.Value(ctxKeyClaims).(*auth.Claims)
		if claims == nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		tenantID := s.resolveTenantID(claims)
		if tenantID == "" {
			writeJSONErr(w, http.StatusForbidden, "forbidden", "no tenant in claims")
			return
		}

		// Path is /v1/projects/:slug/screens/:id/png. Go 1.22+ method-prefix
		// routing exposes path values via r.PathValue.
		slug := r.PathValue("slug")
		screenID := r.PathValue("id")
		if slug == "" || screenID == "" {
			http.Error(w, "missing path params", http.StatusBadRequest)
			return
		}

		// Repo enforces tenant scoping. Cross-tenant reads return ErrNotFound
		// → 404 below (NOT 403) so existence-oracle attacks fail.
		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		info, err := repo.GetScreenForServe(ctx, slug, screenID)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("png handler: repo lookup failed",
				"err", err, "tenant", tenantID, "slug", slug, "screen", screenID)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if info.PngStorageKey == "" {
			// Pipeline hasn't persisted a PNG for this screen yet (still pending),
			// or the export failed before render. Same 404 — no need to leak the
			// difference between "missing" and "asset not yet captured".
			http.NotFound(w, r)
			return
		}

		// Resolve storage key to an absolute path under DataDir/screens. Defense:
		// even though storage keys are server-generated and screen_id is UUID,
		// reject any path that escapes the screens base dir. filepath.Clean
		// resolves "..", and HasPrefix on the absolute path catches symlink
		// shenanigans (in case operator places a symlink inside data/screens/).
		baseDir, err := filepath.Abs(filepath.Join(s.deps.DataDir, "screens"))
		if err != nil {
			slog.Error("png handler: abs base dir", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.Clean(info.PngStorageKey)))
		if err != nil {
			slog.Error("png handler: abs full path", "err", err, "key", info.PngStorageKey)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if !strings.HasPrefix(fullPath, baseDir+string(os.PathSeparator)) && fullPath != baseDir {
			// Storage key tried to escape the base dir. Should never happen
			// with server-generated keys; treated as a hard error.
			slog.Warn("png handler: path traversal rejected",
				"key", info.PngStorageKey, "fullPath", fullPath, "baseDir", baseDir,
				"tenant", tenantID, "screen", screenID)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Open the file. Missing file = 404 (asset deleted by ops or partial
		// failure during pipeline rollback) with a log line so SRE can spot it.
		f, err := os.Open(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("png handler: file missing on disk",
				"path", fullPath, "tenant", tenantID, "screen", screenID)
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("png handler: open failed", "err", err, "path", fullPath)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		st, err := f.Stat()
		if err != nil {
			slog.Error("png handler: stat failed", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "inline")

		if _, err := io.Copy(w, f); err != nil {
			// Connection likely dropped mid-stream — log at debug; nothing to
			// recover at this point (headers already sent).
			slog.Debug("png handler: copy failed (client disconnect?)",
				"err", err, "path", fullPath)
		}
	}
}
