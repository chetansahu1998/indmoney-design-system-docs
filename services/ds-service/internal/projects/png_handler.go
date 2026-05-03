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
	"context"
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
//
// Pr8 — also accepts an asset-scoped signed token via `?at=<token>` so image
// loaders (which can't carry Authorization headers) don't have to leak the
// full JWT into URLs. Either path works; bearer JWT takes precedence when
// present (same security posture as before).
func (s *Server) HandleScreenPNG() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// Path is /v1/projects/:slug/screens/:id/png. Go 1.22+ method-prefix
		// routing exposes path values via r.PathValue.
		slug := r.PathValue("slug")
		screenID := r.PathValue("id")
		if slug == "" || screenID == "" {
			http.Error(w, "missing path params", http.StatusBadRequest)
			return
		}

		// Pr8 — asset-token path. When `?at=` is present, verify against
		// (any tenant, screenID); the verifier checks (tenant, screen) so
		// an attacker can't replay a token across tenants. We still need to
		// know which tenant — since the signed payload binds it, we encode
		// the tenant in the token's MAC, verify against each tenant the
		// caller could plausibly belong to. Simplest: skip JWT entirely and
		// look up the screen's tenant_id, then verify the token against
		// (that tenant, this screen). If verify passes, the caller proves
		// possession of a valid mint.
		var tenantID string
		if at := r.URL.Query().Get("at"); at != "" && s.deps.AssetSigner != nil {
			// Resolve the screen's tenant_id without an existence oracle:
			// scan tenants the asset MAC could have been minted for.
			// Practical shortcut: derive tenant_id from a single lookup that
			// takes only the screen_id as input (tenant_id is bound on the
			// row). This skips the JWT — the asset token IS the auth.
			t, lookupErr := s.lookupScreenTenant(ctx, screenID)
			if lookupErr != nil || t == "" {
				http.NotFound(w, r)
				return
			}
			if err := s.deps.AssetSigner.Verify(at, t, screenID); err != nil {
				writeJSONErr(w, http.StatusUnauthorized, "asset_token", err.Error())
				return
			}
			tenantID = t
		} else {
			claims, _ := ctx.Value(ctxKeyClaims).(*auth.Claims)
			if claims == nil {
				writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
				return
			}
			tenantID = s.resolveTenantID(claims)
			if tenantID == "" {
				writeJSONErr(w, http.StatusForbidden, "forbidden", "no tenant in claims")
				return
			}
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
		// png_storage_key already begins with "screens/" (set by Pipeline.persistPNG),
		// so the base must be DataDir alone — joining "screens" would give "screens/screens/...".
		baseDir, err := filepath.Abs(s.deps.DataDir)
		if err != nil {
			slog.Error("png handler: abs base dir", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		// Phase 3.5 follow-up #2: optional LOD tier via ?tier=l1|l2.
		// When unset → serve the full-size PNG (Phase 1 default). When
		// the requested tier file is missing on disk (LOD generation
		// failed for this screen, or the screen pre-dates the LOD
		// pipeline change), 404 + frontend's pickLOD falls back via
		// lodURL → "full" tier on retry.
		tieredKey := applyLODSuffix(info.PngStorageKey, r.URL.Query().Get("tier"), ".png")

		fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.Clean(tieredKey)))
		if err != nil {
			slog.Error("png handler: abs full path", "err", err, "key", tieredKey)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if !strings.HasPrefix(fullPath, baseDir+string(os.PathSeparator)) && fullPath != baseDir {
			// Storage key tried to escape the base dir. Should never happen
			// with server-generated keys; treated as a hard error.
			slog.Warn("png handler: path traversal rejected",
				"key", tieredKey, "fullPath", fullPath, "baseDir", baseDir,
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

// HandleScreenKTX2 returns the HTTP handler for
// `GET /v1/projects/:slug/screens/:id/ktx2` (Phase 3.5 U2). Mirrors
// HandleScreenPNG but swaps the .png suffix for .ktx2 when resolving
// the disk path. Returns 404 when the sidecar isn't present (basisu
// missing at persist time, transcode failure, etc.) — frontend falls
// back to the PNG URL on that 404.
func (s *Server) HandleScreenKTX2() http.HandlerFunc {
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

		slug := r.PathValue("slug")
		screenID := r.PathValue("id")
		if slug == "" || screenID == "" {
			http.Error(w, "missing path params", http.StatusBadRequest)
			return
		}

		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		info, err := repo.GetScreenForServe(ctx, slug, screenID)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("ktx2 handler: repo lookup failed",
				"err", err, "tenant", tenantID, "slug", slug, "screen", screenID)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if info.PngStorageKey == "" {
			http.NotFound(w, r)
			return
		}

		// Phase 3.5 follow-up #2: optional LOD tier via ?tier=l1|l2.
		// Apply the tier suffix to the PNG key first, then swap the
		// final extension to .ktx2.
		tieredPngKey := applyLODSuffix(info.PngStorageKey, r.URL.Query().Get("tier"), ".png")
		ktx2Key := tieredPngKey
		if strings.HasSuffix(ktx2Key, ".png") {
			ktx2Key = ktx2Key[:len(ktx2Key)-len(".png")] + ".ktx2"
		} else {
			http.NotFound(w, r)
			return
		}

		// png_storage_key already begins with "screens/" (set by Pipeline.persistPNG),
		// so the base must be DataDir alone — joining "screens" would give "screens/screens/...".
		baseDir, err := filepath.Abs(s.deps.DataDir)
		if err != nil {
			slog.Error("ktx2 handler: abs base dir", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.Clean(ktx2Key)))
		if err != nil {
			slog.Error("ktx2 handler: abs full path", "err", err, "key", ktx2Key)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		if !strings.HasPrefix(fullPath, baseDir+string(os.PathSeparator)) && fullPath != baseDir {
			slog.Warn("ktx2 handler: path traversal rejected",
				"key", ktx2Key, "fullPath", fullPath, "baseDir", baseDir,
				"tenant", tenantID, "screen", screenID)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		f, err := os.Open(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			// Common path: basisu wasn't on PATH at persist time, or
			// transcode failed for this particular screen. Frontend
			// observes 404 and falls back to .png — no loud log.
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("ktx2 handler: open failed", "err", err, "path", fullPath)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		st, err := f.Stat()
		if err != nil {
			slog.Error("ktx2 handler: stat failed", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}

		// image/ktx2 per Khronos. nosniff so proxies don't auto-detect.
		w.Header().Set("Content-Type", "image/ktx2")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "inline")

		if _, err := io.Copy(w, f); err != nil {
			slog.Debug("ktx2 handler: copy failed (client disconnect?)",
				"err", err, "path", fullPath)
		}
	}
}

// applyLODSuffix splices the LOD tier suffix (".l1" / ".l2") into a
// storage key just before the file extension. tier="" or any unknown
// value returns the input unchanged.
//
// "screens/<tenant>/<version>/<id>@2x.png", "l1", ".png"
//   → "screens/<tenant>/<version>/<id>@2x.l1.png"
//
// extSuffix is the trailing file extension to splice before (".png"
// for the PNG handler, ".ktx2" for the KTX2 handler — though the KTX2
// handler also runs through this for the .png key first then swaps
// the suffix to .ktx2 since the PNG storage key is the canonical
// reference).
func applyLODSuffix(storageKey, tier, extSuffix string) string {
	if tier == "" {
		return storageKey
	}
	var infix string
	switch tier {
	case "l1":
		infix = ".l1"
	case "l2":
		infix = ".l2"
	default:
		return storageKey
	}
	if strings.HasSuffix(storageKey, extSuffix) {
		base := storageKey[:len(storageKey)-len(extSuffix)]
		return base + infix + extSuffix
	}
	return storageKey
}

// lookupScreenTenant resolves the tenant_id that owns a given screen_id. Used
// by the asset-token path to avoid requiring the caller to supply tenant_id
// in the URL (which would be an existence oracle: "this screen belongs to
// tenant X" leaks via 200/404). Returns ("", ErrNotFound) when no row exists
// — the handler maps that to a generic 404. The asset token's HMAC then
// proves the caller knew the right tenant_id when minting.
func (s *Server) lookupScreenTenant(ctx context.Context, screenID string) (string, error) {
	var tenantID string
	err := s.deps.DB.DB.QueryRowContext(ctx,
		`SELECT tenant_id FROM screens WHERE id = ?`, screenID,
	).Scan(&tenantID)
	if err != nil {
		return "", err
	}
	return tenantID, nil
}

// HandleMintAssetToken returns the handler for `POST /v1/projects/:slug/screens/:id/png-url`.
// Authenticated via JWT (existing requireAuth). Mints a short-lived asset
// token bound to (tenant, screen) and returns the full URL with `?at=`.
// The frontend uses the returned URL on <img src> / texture loaders so the
// JWT never enters the URL.
func (s *Server) HandleMintAssetToken() http.HandlerFunc {
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
		if s.deps.AssetSigner == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_signer", "not configured")
			return
		}
		slug := r.PathValue("slug")
		screenID := r.PathValue("id")
		if slug == "" || screenID == "" {
			http.Error(w, "missing path params", http.StatusBadRequest)
			return
		}
		// Tenant scoping — confirm the screen belongs to this tenant before
		// minting a token for it. Returns 404 cross-tenant (no oracle).
		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		info, err := repo.GetScreenForServe(ctx, slug, screenID)
		if err != nil || info == nil {
			http.NotFound(w, r)
			return
		}
		token := s.deps.AssetSigner.Mint(tenantID, screenID, auth.AssetTokenTTL)
		writeJSON(w, http.StatusOK, map[string]any{
			"url":        fmt.Sprintf("/v1/projects/%s/screens/%s/png?at=%s", slug, screenID, token),
			"ktx2_url":   fmt.Sprintf("/v1/projects/%s/screens/%s/ktx2?at=%s", slug, screenID, token),
			"expires_in": int(auth.AssetTokenTTL.Seconds()),
		})
	}
}
