package projects

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// figma_hash.go — U4 of the autosync bridge plan (2026-05-14).
//
// Computes content + position hashes over Figma node subtrees. The
// AutoSyncPlanner uses these to decide:
//   - same content_hash  → no re-export needed
//   - same content, new position_hash → cheap_update (rename flow only)
//   - new content_hash   → full_export through the audit pipeline
//
// Hash ordering rule: descendants are sorted by (depth, node_id) — NOT
// by order_index. Sibling reorder inside Figma isn't a real "content
// change" semantically; the deepening pass for this plan called out
// the spurious-re-export risk if order_index participates.

// EmptySubtreeHash is the sentinel hash for a subtree with zero descendants
// other than the root. Used so the planner can compare with a concrete
// value rather than NULL/empty-string handling everywhere.
const EmptySubtreeHash = "empty"

// HashableNode is the minimum identity a node needs to feed the hash.
// Mirrors FigmaNodeRow fields the inventory poller produces but lets
// tests construct subtrees without a DB.
type HashableNode struct {
	NodeID     string
	ParentID   string
	NodeType   string
	Name       string
	HasBBox    bool
	X          float64
	Y          float64
	Width      float64
	Height     float64
	Depth      int
	OrderIndex int
}

// ComputeContentHash returns the SHA256 of the subtree rooted at
// rootNodeID, EXCLUDING the root's own bbox + name (so a root move
// doesn't flip content_hash). Pass every descendant node in `all`;
// nodes outside the rootNodeID subtree are filtered automatically by
// walking parent_id chains.
//
// Order: descendants sorted by (depth, node_id). Two crawls of the same
// content produce identical hashes regardless of Figma's child-ordering
// jitter across responses.
//
// Returns EmptySubtreeHash when the root has no descendants.
func ComputeContentHash(rootNodeID string, all []HashableNode) string {
	descendants := collectDescendants(rootNodeID, all)
	if len(descendants) == 0 {
		return EmptySubtreeHash
	}
	sort.Slice(descendants, func(i, j int) bool {
		if descendants[i].Depth != descendants[j].Depth {
			return descendants[i].Depth < descendants[j].Depth
		}
		return descendants[i].NodeID < descendants[j].NodeID
	})

	// Stable serialization. Each line: id|type|name|x|y|w|h. The bbox
	// fields are zero when HasBBox is false; collapse to "-" so the
	// has-vs-doesn't-have-bbox distinction is preserved without
	// triggering false-positives on numeric formatting drift.
	var b strings.Builder
	b.Grow(len(descendants) * 64)
	for _, n := range descendants {
		b.WriteString(n.NodeID)
		b.WriteByte('|')
		b.WriteString(n.NodeType)
		b.WriteByte('|')
		b.WriteString(n.Name)
		b.WriteByte('|')
		if n.HasBBox {
			b.WriteString(formatFloat(n.X))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Y))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Width))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Height))
		} else {
			b.WriteString("-|-|-|-")
		}
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ComputePositionHash returns the SHA256 of the node's own identity
// fields: name + bbox + order_index. Used by the planner to detect
// "this node was renamed or moved but its children didn't change"
// vs. a content-change. Compact since one node = one hashable line.
//
// Returns the empty string when the node isn't found.
func ComputePositionHash(rootNodeID string, all []HashableNode) string {
	for _, n := range all {
		if n.NodeID != rootNodeID {
			continue
		}
		var b strings.Builder
		b.WriteString(n.Name)
		b.WriteByte('|')
		if n.HasBBox {
			b.WriteString(formatFloat(n.X))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Y))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Width))
			b.WriteByte('|')
			b.WriteString(formatFloat(n.Height))
		} else {
			b.WriteString("-|-|-|-")
		}
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(n.OrderIndex))
		sum := sha256.Sum256([]byte(b.String()))
		return hex.EncodeToString(sum[:])
	}
	return ""
}

// collectDescendants returns every node in `all` whose ancestor chain
// (via parent_id) eventually reaches rootNodeID. Root itself excluded.
//
// Implementation: build a parent_id → children map once, then BFS.
// O(n) over the input slice.
func collectDescendants(rootNodeID string, all []HashableNode) []HashableNode {
	if rootNodeID == "" {
		return nil
	}
	childrenByParent := make(map[string][]HashableNode, len(all))
	for _, n := range all {
		if n.ParentID == "" {
			continue
		}
		childrenByParent[n.ParentID] = append(childrenByParent[n.ParentID], n)
	}
	var out []HashableNode
	queue := []string{rootNodeID}
	visited := make(map[string]bool, 64)
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if visited[parent] {
			continue
		}
		visited[parent] = true
		for _, child := range childrenByParent[parent] {
			if visited[child.NodeID] {
				continue
			}
			out = append(out, child)
			queue = append(queue, child.NodeID)
		}
	}
	return out
}

// formatFloat renders a float deterministically. Strips trailing zeros
// so 100.0 and 100.00 hash identically.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
