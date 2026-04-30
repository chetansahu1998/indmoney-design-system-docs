package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Persist coordinates writes to lib/audit/<slug>.json + lib/audit-files.json
// + lib/audit/index.json from a single plugin or CLI run. Concurrent plugin
// POSTs against the same audit-server should not corrupt files.
//
// Strategy:
//   • Per-file JSON: atomic write (.tmp → rename), no lock needed because
//     each file_key writes to its own path.
//   • lib/audit-files.json: read-modify-write under a process-wide mutex —
//     two simultaneous appends would otherwise lose one entry.
//   • lib/audit/index.json: rebuilt from disk under the same mutex so it
//     reflects every per-file JSON currently checked in.
//
// All paths are relative to the repo root; callers pass it explicitly
// rather than re-resolving so tests can target a temp dir.

var persistMu sync.Mutex

// PersistResult writes a per-file audit, registers the file in the curated
// manifest if it isn't already there, and rebuilds the index roll-up.
//
// auditOutDir is typically <repo>/lib/audit; manifestPath is
// <repo>/lib/audit-files.json. dsRev is the design-system rev string
// stamped onto the index.
//
// Returns the per-file path and whether the manifest was mutated (used
// by the response to inform the plugin "this file was just registered").
func PersistResult(auditOutDir, manifestPath, dsRev string, result AuditResult, hashes []HashedNode) (string, bool, error) {
	persistMu.Lock()
	defer persistMu.Unlock()

	// Phase 2 U8 — sidecar-writer deprecation. Default OFF in Phase 2; the
	// audit pipeline writes to SQLite via the projects worker instead. Set
	// DS_AUDIT_LEGACY_SIDECARS=1 to re-enable JSON sidecar writes for one-
	// release rollback. Phase 3 removes this flag entirely.
	if os.Getenv("DS_AUDIT_LEGACY_SIDECARS") != "1" {
		return "", false, nil
	}

	perFilePath, err := WritePerFile(auditOutDir, result)
	if err != nil {
		return "", false, fmt.Errorf("write per-file: %w", err)
	}

	registered, err := registerInManifest(manifestPath, result)
	if err != nil {
		return perFilePath, false, fmt.Errorf("register manifest: %w", err)
	}

	if err := rebuildIndex(auditOutDir, manifestPath, dsRev, hashes, result); err != nil {
		return perFilePath, registered, fmt.Errorf("rebuild index: %w", err)
	}

	return perFilePath, registered, nil
}

// registerInManifest adds the file to lib/audit-files.json if its file_key
// isn't already registered. Updates name if the file has been renamed in
// Figma since the last sync. Atomic write.
func registerInManifest(manifestPath string, result AuditResult) (bool, error) {
	if manifestPath == "" || result.FileKey == "" {
		return false, nil
	}
	type entry struct {
		FileKey    string   `json:"file_key"`
		Name       string   `json:"name"`
		Brand      string   `json:"brand"`
		Owner      string   `json:"owner,omitempty"`
		FinalPages []string `json:"final_pages,omitempty"`
	}
	type doc struct {
		Description string  `json:"$description,omitempty"`
		Files       []entry `json:"files"`
	}

	var m doc
	if bs, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(bs, &m)
	}
	if m.Description == "" {
		m.Description = "Auto-registered by the Figma plugin. Each entry comes from a designer running an audit on that file. Edit `final_pages` here to override LooksLikeScreen detection."
	}

	mutated := false
	found := false
	for i := range m.Files {
		if m.Files[i].FileKey == result.FileKey {
			found = true
			if m.Files[i].Name != result.FileName && result.FileName != "" {
				m.Files[i].Name = result.FileName
				mutated = true
			}
			if m.Files[i].Brand == "" && result.Brand != "" {
				m.Files[i].Brand = result.Brand
				mutated = true
			}
			break
		}
	}
	if !found {
		m.Files = append(m.Files, entry{
			FileKey: result.FileKey,
			Name:    result.FileName,
			Brand:   result.Brand,
			Owner:   result.Owner,
		})
		mutated = true
	}
	if !mutated {
		return false, nil
	}
	sort.SliceStable(m.Files, func(i, j int) bool {
		return strings.ToLower(m.Files[i].Name) < strings.ToLower(m.Files[j].Name)
	})
	bs, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	tmp := manifestPath + ".tmp"
	if err := os.WriteFile(tmp, append(bs, '\n'), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, manifestPath); err != nil {
		return false, err
	}
	return true, nil
}

// rebuildIndex reads every per-file JSON in auditOutDir, runs BuildIndex,
// and writes lib/audit/index.json. Hashes for the *current* run are passed
// in; hashes from previously-audited files are not retained on disk yet
// (v1 punt — cross-file pattern detection only sees files audited within
// the same audit-server lifetime). To make cross-file work across plugin
// sessions, persist hashes alongside per-file JSON in v1.1.
func rebuildIndex(auditOutDir, manifestPath, dsRev string, currentHashes []HashedNode, current AuditResult) error {
	results, err := readAllPerFile(auditOutDir, current)
	if err != nil {
		return err
	}
	hashesByFile := map[string][]HashedNode{}
	if current.FileSlug != "" && len(currentHashes) > 0 {
		hashesByFile[current.FileSlug] = currentHashes
	}
	idx := BuildIndex(results, hashesByFile, dsRev)
	_, err = WriteIndex(auditOutDir, idx)
	return err
}

// readAllPerFile loads every <slug>.json in auditOutDir except index.json.
// The currently-running result overrides whatever's on disk for that slug
// (so the freshly-computed in-memory result is what lands in the index).
func readAllPerFile(dir string, current AuditResult) ([]AuditResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditResult{current}, nil
		}
		return nil, err
	}
	out := []AuditResult{}
	seenCurrent := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.json" || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		bs, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r AuditResult
		if err := json.Unmarshal(bs, &r); err != nil {
			continue
		}
		if r.FileSlug == current.FileSlug && current.FileSlug != "" {
			out = append(out, current)
			seenCurrent = true
			continue
		}
		out = append(out, r)
	}
	if !seenCurrent && current.FileSlug != "" {
		out = append(out, current)
	}
	// Stable order — name asc.
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].FileName) < strings.ToLower(out[j].FileName)
	})
	return out, nil
}

// audit-files.json schema knobs reused by the plugin response.
type registeredEntry struct {
	FileKey string `json:"file_key"`
	Name    string `json:"name"`
}

// FilesIndex returns a lightweight summary suitable for the plugin to display.
func FilesIndex(manifestPath string) ([]registeredEntry, error) {
	bs, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Files []registeredEntry `json:"files"`
	}
	if err := json.Unmarshal(bs, &doc); err != nil {
		return nil, err
	}
	return doc.Files, nil
}

// nowRFC is a small helper used in unit tests; keeps the time package imported.
func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }
