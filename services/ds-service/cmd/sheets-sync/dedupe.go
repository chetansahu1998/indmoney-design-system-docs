package main

import "sort"

// dedupe.go — cross-tab dedup. Same (file_id, node_id) appearing in
// multiple tabs collapses per the priority rules in the plan §"High-Level
// Technical Design / Dedup algorithm".
//
// Note: Product Design / Lotties / Illustrations tabs are skipped UPSTREAM
// (parse.go SubSheetToProduct returns Skip=true and the orchestrator drops
// them before this function runs). This is the safety net for collisions
// among the *non-skipped* tabs — rare in production today (0 hits in the
// 2026-05-05 deepening pull) but kept so a future copy-paste mistake
// doesn't double-import.

// NormalizedRow is what the rest of the pipeline operates on after
// per-row parse + classification. One row in the sheet → one
// NormalizedRow regardless of whether it ends up surviving dedupe.
type NormalizedRow struct {
	Tab           string
	RowIndex      int           // 1-based, header is row 1
	Project       string        // sheet column A
	ProductPOC    string        // sheet column B (free text, multi-author OK)
	DesignerPOC   string        // sheet column C
	DRDURL        string        // sheet column D
	DRDKind       DRDKind
	GDocID        string        // populated when DRDKind == DRDGoogleDoc
	FigmaURL      string        // sheet column E
	URLKind       URLKind
	FileID        string        // populated when URLKind == URLValid
	NodeID        string
	ProtoURL      string        // sheet column F (passed through to flow metadata)
	StatusRaw     string        // sheet column G raw
	StatusNorm    string        // normalized via NormalizeStatus
	LastUpdated   string        // sheet column H raw
	Mapping       SubSheetMapping // resolved tab → product/lobe
	RowHash       string          // computed in U7
}

// Dedupe collapses NormalizedRows by (file_id, node_id) per the
// priority rules. Empty file_id / node_id (ghost rows + canvas-only
// + malformed) pass through untouched — they're never duplicates.
//
// Priority within a (file_id, node_id) bucket:
//   1. explicit-tab + same-tab     → keep the row with lower row_index, log warning for the rest
//   2. explicit-tab vs default-tab → drop the default-tab entry, keep explicit
//   3. two explicit tabs           → keep BOTH (legitimate cross-product reference)
//   4. only default-tab entries    → keep the lowest row_index among them
//
// Returns the surviving rows in their original order (sort-stable on tab + row_index)
// and a list of warnings for the orchestrator to emit as telemetry.
func Dedupe(in []NormalizedRow) (out []NormalizedRow, warnings []string) {
	// Bucket the rows that have a real (file_id, node_id) key. Pass-through
	// keys for everything else (ghosts + canvas-only + malformed).
	type key struct{ FileID, NodeID string }
	buckets := map[key][]int{}
	for i, r := range in {
		if r.FileID == "" || r.NodeID == "" {
			continue
		}
		k := key{r.FileID, r.NodeID}
		buckets[k] = append(buckets[k], i)
	}

	keep := make([]bool, len(in))
	// First pass: anything not bucketed (ghost / canvas-only / malformed) keeps.
	for i, r := range in {
		if r.FileID == "" || r.NodeID == "" {
			keep[i] = true
		}
	}

	// Second pass: bucket-by-bucket dedup.
	for _, indices := range buckets {
		if len(indices) == 1 {
			keep[indices[0]] = true
			continue
		}

		// Group by tab to detect same-tab duplicates first.
		byTab := map[string][]int{}
		for _, idx := range indices {
			byTab[in[idx].Tab] = append(byTab[in[idx].Tab], idx)
		}
		// Same-tab duplicates: keep lowest row_index per tab, warn on the rest.
		representative := []int{}
		for tab, list := range byTab {
			if len(list) > 1 {
				sort.Slice(list, func(i, j int) bool { return in[list[i]].RowIndex < in[list[j]].RowIndex })
				keep[list[0]] = true
				representative = append(representative, list[0])
				for _, dropped := range list[1:] {
					warnings = append(warnings, sameTabWarning(tab, in[dropped]))
				}
			} else {
				keep[list[0]] = true
				representative = append(representative, list[0])
			}
		}

		// Cross-tab: explicit beats default. If we have at least one explicit,
		// drop the defaults (they're lower-priority noise).
		hasExplicit := false
		for _, idx := range representative {
			if in[idx].Mapping.Source == "explicit" {
				hasExplicit = true
				break
			}
		}
		if hasExplicit {
			for _, idx := range representative {
				if in[idx].Mapping.Source != "explicit" {
					keep[idx] = false
					warnings = append(warnings, dropDefaultWarning(in[idx]))
				}
			}
		}
		// If two or more EXPLICIT tabs share the same key, keep all (legitimate).
		// Nothing to do — all already keep[].
	}

	// Build output in original order.
	for i, k := range keep {
		if k {
			out = append(out, in[i])
		}
	}
	return out, warnings
}

func sameTabWarning(tab string, r NormalizedRow) string {
	return "same-tab duplicate dropped: tab=" + tab +
		" row=" + itoa(r.RowIndex) +
		" project=" + r.Project +
		" file=" + r.FileID +
		" node=" + r.NodeID
}

func dropDefaultWarning(r NormalizedRow) string {
	return "default-tab dropped in favor of explicit-tab: tab=" + r.Tab +
		" row=" + itoa(r.RowIndex) +
		" project=" + r.Project
}

// itoa avoids pulling strconv just for warning string construction; cmd
// import set is intentionally tight.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
