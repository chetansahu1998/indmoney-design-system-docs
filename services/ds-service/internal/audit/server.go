package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// AuditRequest is the body the Figma plugin POSTs to /v1/audit/run.
type AuditRequest struct {
	NodeTree map[string]any `json:"node_tree"`
	Scope    string         `json:"scope"`     // "selection" | "page" | "file"
	FileKey  string         `json:"file_key"`
	FileName string         `json:"file_name,omitempty"`
	Brand    string         `json:"brand,omitempty"`
}

// AuditResponse is the wire shape returned to the plugin.
type AuditResponse struct {
	SchemaVersion string      `json:"schema_version"`
	CacheKey      string      `json:"cache_key"` // file_key + ":" + ds_rev
	Result        AuditResult `json:"result"`
	// Registered is true when this audit-server run added (or mutated)
	// an entry in lib/audit-files.json. The plugin uses it to nudge
	// the designer ("This file is now tracked — run `git diff` to ship.").
	Registered     bool   `json:"registered"`
	PersistedPath  string `json:"persisted_path,omitempty"`
}

// HandlerConfig wires the audit endpoint's runtime knobs without coupling
// to the larger server's config. Repo-relative paths so the handler works
// when the binary is run from any cwd.
type HandlerConfig struct {
	RepoRoot string
}

// HandleAudit returns an http.HandlerFunc serving POST /v1/audit/run.
// CORS preflight is handled separately by the server's middleware; this
// handler only deals with POST.
//
// Error semantics:
//   400 — body parse failure / unknown scope
//   413 — node tree too large (> 50 MB)
//   500 — token-load failure on the host (designer should re-run npm install)
func HandleAudit(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Cap body size — Figma plugin should never send > a few MB,
		// but guard the server.
		r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
		var req AuditRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("parse body: %s", err), http.StatusBadRequest)
			return
		}
		if req.NodeTree == nil {
			http.Error(w, "node_tree is required", http.StatusBadRequest)
			return
		}

		brand := req.Brand
		if brand == "" {
			brand = "indmoney"
		}

		tokens, err := loadTokensFromRepo(cfg.RepoRoot, brand)
		if err != nil {
			http.Error(w, fmt.Sprintf("load tokens: %s", err), http.StatusInternalServerError)
			return
		}
		candidates, _ := loadComponentsFromRepo(cfg.RepoRoot)

		dsRev, _ := designSystemRevFromRepo(cfg.RepoRoot)

		opts := Options{
			FileKey:         req.FileKey,
			FileName:        req.FileName,
			FileSlug:        slugifyForFileKey(req.FileKey, req.FileName),
			Brand:           brand,
			DesignSystemRev: dsRev,
		}
		result := Audit(req.NodeTree, tokens, candidates, opts)
		hashes := CollectHashes(req.NodeTree)

		resp := AuditResponse{
			SchemaVersion: SchemaVersion,
			CacheKey:      req.FileKey + ":" + dsRev,
			Result:        result,
		}

		// Persist when scope is "file" — a partial-tree audit (selection
		// or page) shouldn't overwrite the canonical per-file artifact.
		if req.Scope == "file" {
			persistedPath, registered, err := PersistResult(
				filepath.Join(cfg.RepoRoot, "lib/audit"),
				filepath.Join(cfg.RepoRoot, "lib/audit-files.json"),
				dsRev,
				result,
				hashes,
			)
			if err != nil {
				// Persist failures shouldn't block the plugin — return the
				// audit result and surface the error in $extensions.
				if resp.Result.Extensions == nil {
					resp.Result.Extensions = map[string]any{}
				}
				resp.Result.Extensions["com.indmoney.persistError"] = err.Error()
			}
			resp.PersistedPath = persistedPath
			resp.Registered = registered
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// slugifyForFileKey turns the file_key + name into the on-disk slug used by
// lib/audit/<slug>.json. Prefers a name-based slug because it's human-readable;
// falls back to the file_key if name is empty.
func slugifyForFileKey(fileKey, name string) string {
	src := name
	if src == "" {
		src = fileKey
	}
	var b []rune
	prevDash := false
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b = append(b, r+('a'-'A'))
			prevDash = false
		default:
			if !prevDash && len(b) > 0 {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if out == "" {
		return fileKey
	}
	return out
}

// designSystemRevFromRepo computes sha256(8) of the published icons manifest.
func designSystemRevFromRepo(root string) (string, error) {
	bs, err := os.ReadFile(filepath.Join(root, "public/icons/glyph/manifest.json"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bs)
	return hex.EncodeToString(sum[:8]), nil
}

// loadTokensFromRepo reads + flattens the published DS color + dimension tokens.
// Mirrors the cmd/audit loader to keep one schema reader, but local to this
// package so the handler doesn't import cmd/audit (which would create a cycle).
func loadTokensFromRepo(root, brand string) ([]DSToken, error) {
	tokens := []DSToken{}
	for _, name := range []string{"semantic.tokens.json", "base.tokens.json"} {
		bs, err := os.ReadFile(filepath.Join(root, "lib/tokens", brand, name))
		if err != nil {
			continue
		}
		tokens = append(tokens, flattenColorTokens(bs)...)
	}
	if bs, err := os.ReadFile(filepath.Join(root, "lib/tokens", brand, "spacing.tokens.json")); err == nil {
		tokens = append(tokens, flattenDimensionTokens(bs)...)
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no tokens loaded for brand %q", brand)
	}
	return tokens, nil
}

func loadComponentsFromRepo(root string) ([]DSCandidate, error) {
	bs, err := os.ReadFile(filepath.Join(root, "public/icons/glyph/manifest.json"))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Icons []struct {
			Slug        string `json:"slug"`
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			SetID       string `json:"set_id"`
			Key         string `json:"key"`
			Description string `json:"description"`
			VariantAxes []struct {
				Name string `json:"name"`
			} `json:"variant_axes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(bs, &raw); err != nil {
		return nil, err
	}
	out := []DSCandidate{}
	for _, i := range raw.Icons {
		if i.Kind != "component" {
			continue
		}
		out = append(out, DSCandidate{
			Slug:         i.Slug,
			Name:         i.Name,
			ComponentKey: i.SetID,
			SetKey:       i.Key,
			Description:  i.Description,
			AxisCount:    len(i.VariantAxes),
		})
	}
	return out, nil
}

// — Local DTCG flatteners (duplicated from cmd/audit so this package is
//   self-contained; small enough to keep in sync by hand).

func flattenColorTokens(raw []byte) []DSToken {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := []DSToken{}
	walkDTCG(doc, "", func(path string, leaf map[string]any) {
		if t, _ := leaf["$type"].(string); t != "color" && t != "" {
			return
		}
		hexStr := dtcgColorToHex(leaf["$value"])
		if hexStr == "" {
			return
		}
		ext, _ := leaf["$extensions"].(map[string]any)
		ind, _ := ext["com.indmoney"].(map[string]any)
		dep, _ := ind["deprecated"].(bool)
		repl, _ := ind["replacedBy"].(string)
		figmaName, _ := ext["com.indmoney.figma-name"].(string)
		figmaCol, _ := ext["com.indmoney.figma-collection"].(string)
		if figmaName == "" {
			figmaName, _ = ind["figma-name"].(string)
		}
		if figmaCol == "" {
			figmaCol, _ = ind["figma-collection"].(string)
		}
		out = append(out, DSToken{
			Path:            path,
			Hex:             hexStr,
			Kind:            "color",
			Deprecated:      dep,
			ReplacedBy:      repl,
			FigmaName:       figmaName,
			FigmaCollection: figmaCol,
		})
	})
	return out
}

func flattenDimensionTokens(raw []byte) []DSToken {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := []DSToken{}
	walkDTCG(doc, "", func(path string, leaf map[string]any) {
		if t, _ := leaf["$type"].(string); t != "dimension" {
			return
		}
		val, _ := leaf["$value"].(map[string]any)
		v, _ := val["value"].(float64)
		kind := "spacing"
		switch {
		case len(path) > 7 && path[:7] == "radius.":
			kind = "radius"
		case len(path) > 8 && path[:8] == "padding.":
			kind = "padding"
		}
		out = append(out, DSToken{Path: path, Px: v, Kind: kind})
	})
	return out
}

func walkDTCG(node any, prefix string, fn func(string, map[string]any)) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if _, hasValue := m["$value"]; hasValue {
		fn(prefix, m)
		return
	}
	for k, v := range m {
		if len(k) > 0 && k[0] == '$' {
			continue
		}
		next := k
		if prefix != "" {
			next = prefix + "." + k
		}
		walkDTCG(v, next, fn)
	}
}

func dtcgColorToHex(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	comps, _ := m["components"].([]any)
	if len(comps) < 3 {
		return ""
	}
	r, _ := comps[0].(float64)
	g, _ := comps[1].(float64)
	b, _ := comps[2].(float64)
	return RGBToHex(r, g, b)
}
