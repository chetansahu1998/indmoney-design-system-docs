package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// WritePerFile serializes one AuditResult to <outDir>/<file_slug>.json (atomic).
func WritePerFile(outDir string, r AuditResult) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, r.FileSlug+".json")
	tmp := path + ".tmp"
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// HashedNode pairs a canonical hash with its node id, one bucket per file.
type HashedNode struct {
	Hash   string
	NodeID string
}

// BuildIndex produces the lib/audit/index.json roll-up across every per-file
// result. Computes cross-file canonical-hash patterns + token usage rollup
// + component usage rollup.
func BuildIndex(results []AuditResult, hashesByFile map[string][]HashedNode, dsRev string) Index {
	idx := Index{
		SchemaVersion:   SchemaVersion,
		GeneratedAt:     time.Now().UTC(),
		DesignSystemRev: dsRev,
		Extensions: map[string]any{
			"com.indmoney.provenance":  "figma-audit-index",
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}

	tokenAggregate := map[string]*TokenUsage{}
	tokenFileSet := map[string]map[string]bool{}
	componentAggregate := map[string]*ComponentUsage{}
	componentFileSet := map[string]map[string]bool{}
	hashBucket := map[string]map[string]bool{} // canonicalHash → set of fileSlugs
	hashTotal := map[string]int{}

	for _, r := range results {
		idx.Files = append(idx.Files, IndexEntry{
			FileKey:          r.FileKey,
			FileName:         r.FileName,
			FileSlug:         r.FileSlug,
			Brand:            r.Brand,
			ExtractedAt:      r.ExtractedAt,
			OverallCoverage:  r.OverallCoverage,
			OverallFromDS:    r.OverallFromDS,
			ScreenCount:      len(r.Screens),
			HeadlineDriftHex: r.HeadlineDriftHex,
		})

		for _, s := range r.Screens {
			for _, f := range s.Fixes {
				if f.TokenPath == "" {
					continue
				}
				u, ok := tokenAggregate[f.TokenPath]
				if !ok {
					u = &TokenUsage{TokenPath: f.TokenPath}
					tokenAggregate[f.TokenPath] = u
					tokenFileSet[f.TokenPath] = map[string]bool{}
				}
				u.UsageCount++
				tokenFileSet[f.TokenPath][r.FileSlug] = true
				if len(u.UseSites) < 5 {
					u.UseSites = append(u.UseSites, TokenUseSite{
						FileSlug:   r.FileSlug,
						ScreenSlug: s.Slug,
						NodeID:     f.NodeID,
						NodeName:   f.NodeName,
					})
				}
			}
			for _, m := range s.ComponentMatches {
				if m.MatchedSlug == "" {
					continue
				}
				cu, ok := componentAggregate[m.MatchedSlug]
				if !ok {
					cu = &ComponentUsage{Slug: m.MatchedSlug}
					componentAggregate[m.MatchedSlug] = cu
					componentFileSet[m.MatchedSlug] = map[string]bool{}
				}
				cu.UsageCount++
				componentFileSet[m.MatchedSlug][r.FileSlug] = true
			}
		}

		for _, h := range hashesByFile[r.FileSlug] {
			if hashBucket[h.Hash] == nil {
				hashBucket[h.Hash] = map[string]bool{}
			}
			hashBucket[h.Hash][r.FileSlug] = true
			hashTotal[h.Hash]++
		}
	}

	for path, u := range tokenAggregate {
		u.FileCount = len(tokenFileSet[path])
		idx.TokenUsage = append(idx.TokenUsage, *u)
	}
	for slug, cu := range componentAggregate {
		cu.FileCount = len(componentFileSet[slug])
		idx.ComponentUsage = append(idx.ComponentUsage, *cu)
	}
	sort.Slice(idx.TokenUsage, func(i, j int) bool {
		return idx.TokenUsage[i].UsageCount > idx.TokenUsage[j].UsageCount
	})
	sort.Slice(idx.ComponentUsage, func(i, j int) bool {
		return idx.ComponentUsage[i].UsageCount > idx.ComponentUsage[j].UsageCount
	})

	for hash, files := range hashBucket {
		if len(files) < 2 {
			continue
		}
		fileList := make([]string, 0, len(files))
		for f := range files {
			fileList = append(fileList, f)
		}
		sort.Strings(fileList)
		idx.CrossFilePatterns = append(idx.CrossFilePatterns, CrossFilePattern{
			CanonicalHash: hash,
			NodeCount:     hashTotal[hash],
			Files:         fileList,
			SuggestedName: hash,
		})
	}
	sort.Slice(idx.CrossFilePatterns, func(i, j int) bool {
		return idx.CrossFilePatterns[i].NodeCount > idx.CrossFilePatterns[j].NodeCount
	})
	return idx
}

// WriteIndex serializes the index to <outDir>/index.json (atomic).
func WriteIndex(outDir string, idx Index) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "index.json")
	tmp := path + ".tmp"
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// CollectHashes walks a Figma node tree and returns canonical hashes for
// every FRAME / COMPONENT / INSTANCE / COMPONENT_SET — the granularity at
// which cross-file pattern detection is meaningful.
func CollectHashes(tree map[string]any) []HashedNode {
	out := []HashedNode{}
	walk(tree, func(n map[string]any) {
		t, _ := n["type"].(string)
		switch t {
		case "FRAME", "COMPONENT", "INSTANCE", "COMPONENT_SET":
			id, _ := n["id"].(string)
			h := CanonicalHash(n)
			if h == "" {
				return
			}
			out = append(out, HashedNode{Hash: h, NodeID: id})
		}
	})
	return out
}
