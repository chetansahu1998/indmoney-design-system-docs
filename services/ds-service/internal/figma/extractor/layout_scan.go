// Layout pattern scanner — walks Figma node trees and harvests auto-layout
// and corner-radius values that componentry actually uses, then histograms
// them into a "discovered token scale".
//
// Why this exists: Glyph doesn't expose Variables on the current PAT plan,
// but every FRAME/COMPONENT/INSTANCE node carries the actual numbers used by
// the design — `itemSpacing`, `paddingLeft/Right/Top/Bottom`, `cornerRadius`.
// Repeated values across many components are de-facto tokens; this scanner
// surfaces them.
//
// Output: a frequency-sorted scale per dimension (spacing / padding / radius),
// plus the raw histogram so callers can visualize token density.
package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// LayoutHistogram maps a numeric value (px) to the count of nodes using it.
type LayoutHistogram map[float64]int

// LayoutPatterns is the result of one scan.
type LayoutPatterns struct {
	Spacing    LayoutHistogram // itemSpacing across auto-layout frames
	Padding    LayoutHistogram // paddingLeft/Right/Top/Bottom flattened
	Radius     LayoutHistogram // cornerRadius (skips per-corner overrides)
	NodesSeen  int
	FramesSeen int
	Pages      []string // node IDs scanned, for provenance
}

// MergeInto adds rhs into lhs (in place).
func (h LayoutHistogram) MergeInto(rhs LayoutHistogram) {
	for k, v := range rhs {
		h[k] += v
	}
}

// Sorted returns the histogram as (value, count) pairs sorted ascending by value.
type ValueCount struct {
	Value float64
	Count int
}

func (h LayoutHistogram) Sorted() []ValueCount {
	out := make([]ValueCount, 0, len(h))
	for v, c := range h {
		out = append(out, ValueCount{Value: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// SortedByCount returns pairs sorted by count desc (most-used first).
func (h LayoutHistogram) SortedByCount() []ValueCount {
	out := h.Sorted()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// MultiFileLayoutPatterns is the cross-file rollup. Aggregate histograms
// behave like single-file ones; PerFile retains per-source counts so the
// emitter can surface "value 16 used 312× across 9 files (insta-plus: 47,
// dashboard-v5: 38, ...)" provenance.
type MultiFileLayoutPatterns struct {
	Aggregate *LayoutPatterns
	// PerFile maps file_slug → per-file pattern result. Order preserved
	// via Files slice (slugs in walk order).
	PerFile map[string]*LayoutPatterns
	Files   []FileWalkSummary
}

// FileWalkSummary is one row in the multi-file walk: which file was scanned,
// what its slug is, and headline counts so callers can log a status line.
type FileWalkSummary struct {
	FileKey    string
	FileSlug   string
	Name       string
	Pages      []string
	NodesSeen  int
	FramesSeen int
	Failed     bool
	FailErr    string
}

// FileWalkInput defines one file the multi-walker should scan. Pages is
// optional — when empty, the walker fetches the file root and walks every
// page that LooksLikeFinal at the page level (caller's responsibility to
// curate, since heuristics belong upstream).
type FileWalkInput struct {
	FileKey  string
	FileSlug string
	Name     string
	Pages    []string // node IDs to walk; empty = all pages from file root
}

// RunLayoutPatternsMultiFile walks every input file, aggregates the per-file
// histograms into a cross-file rollup, and retains the per-file breakdown
// alongside it. A failure in one file logs and continues — the aggregate
// reflects whatever succeeded.
func RunLayoutPatternsMultiFile(ctx context.Context, c *client.Client, inputs []FileWalkInput, log *slog.Logger) (*MultiFileLayoutPatterns, error) {
	out := &MultiFileLayoutPatterns{
		Aggregate: &LayoutPatterns{
			Spacing: LayoutHistogram{},
			Padding: LayoutHistogram{},
			Radius:  LayoutHistogram{},
		},
		PerFile: map[string]*LayoutPatterns{},
		Files:   make([]FileWalkSummary, 0, len(inputs)),
	}
	for _, in := range inputs {
		summary := FileWalkSummary{
			FileKey:  in.FileKey,
			FileSlug: in.FileSlug,
			Name:     in.Name,
			Pages:    in.Pages,
		}
		log.Info("multi-file walk: scanning", "name", in.Name, "file_key", in.FileKey, "pages", len(in.Pages))

		var per *LayoutPatterns
		var err error
		if len(in.Pages) > 0 {
			per, err = RunLayoutPatterns(ctx, c, in.FileKey, in.Pages, log)
		} else {
			per, err = runLayoutPatternsWholeFile(ctx, c, in.FileKey, log)
		}
		if err != nil {
			summary.Failed = true
			summary.FailErr = err.Error()
			out.Files = append(out.Files, summary)
			log.Error("multi-file walk: file failed", "name", in.Name, "err", err)
			continue
		}
		summary.NodesSeen = per.NodesSeen
		summary.FramesSeen = per.FramesSeen
		out.PerFile[in.FileSlug] = per
		out.Files = append(out.Files, summary)

		out.Aggregate.Spacing.MergeInto(per.Spacing)
		out.Aggregate.Padding.MergeInto(per.Padding)
		out.Aggregate.Radius.MergeInto(per.Radius)
		out.Aggregate.NodesSeen += per.NodesSeen
		out.Aggregate.FramesSeen += per.FramesSeen
	}
	// Aggregate.Pages is the union of every walked page id (deduped).
	seen := map[string]bool{}
	for _, f := range out.Files {
		for _, p := range f.Pages {
			if seen[p] {
				continue
			}
			seen[p] = true
			out.Aggregate.Pages = append(out.Aggregate.Pages, p)
		}
	}
	log.Info("multi-file walk complete",
		"files", len(inputs),
		"successful", len(out.PerFile),
		"frames", out.Aggregate.FramesSeen,
		"spacing_values", len(out.Aggregate.Spacing),
		"padding_values", len(out.Aggregate.Padding),
		"radius_values", len(out.Aggregate.Radius),
	)
	return out, nil
}

// runLayoutPatternsWholeFile fetches the file root and walks every page.
// Used when caller didn't pre-curate page ids.
func runLayoutPatternsWholeFile(ctx context.Context, c *client.Client, fileKey string, log *slog.Logger) (*LayoutPatterns, error) {
	out := &LayoutPatterns{
		Spacing: LayoutHistogram{},
		Padding: LayoutHistogram{},
		Radius:  LayoutHistogram{},
	}
	raw, err := c.GetFile(ctx, fileKey, 0)
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", fileKey, err)
	}
	doc, _ := raw["document"].(map[string]any)
	if doc == nil {
		return out, nil
	}
	walkLayout(doc, out)
	log.Info("whole-file layout scan",
		"file_key", fileKey,
		"nodes", out.NodesSeen,
		"frames", out.FramesSeen,
	)
	return out, nil
}

// RunLayoutPatterns scans the given page node IDs and returns merged histograms.
func RunLayoutPatterns(ctx context.Context, c *client.Client, fileKey string, pageNodeIDs []string, log *slog.Logger) (*LayoutPatterns, error) {
	out := &LayoutPatterns{
		Spacing: LayoutHistogram{},
		Padding: LayoutHistogram{},
		Radius:  LayoutHistogram{},
		Pages:   pageNodeIDs,
	}
	for _, nid := range pageNodeIDs {
		log.Info("scanning layout patterns", "page", nid)
		resp, err := c.GetFileNodes(ctx, fileKey, []string{nid}, 0)
		if err != nil {
			return nil, fmt.Errorf("get page %s: %w", nid, err)
		}
		nodes, _ := resp["nodes"].(map[string]any)
		var doc map[string]any
		for _, v := range nodes {
			if m, ok := v.(map[string]any); ok && m != nil {
				doc, _ = m["document"].(map[string]any)
				break
			}
		}
		if doc == nil {
			log.Warn("page returned no document", "page", nid)
			continue
		}
		walkLayout(doc, out)
	}
	log.Info("layout scan complete",
		"nodes", out.NodesSeen,
		"frames", out.FramesSeen,
		"spacing_values", len(out.Spacing),
		"padding_values", len(out.Padding),
		"radius_values", len(out.Radius),
	)
	return out, nil
}

func walkLayout(node map[string]any, out *LayoutPatterns) {
	if node == nil {
		return
	}
	out.NodesSeen++
	t := stringKey(node, "type")

	// Auto-layout values are only meaningful on FRAME / COMPONENT / COMPONENT_SET / INSTANCE
	if t == "FRAME" || t == "COMPONENT" || t == "COMPONENT_SET" || t == "INSTANCE" {
		out.FramesSeen++
		recordIfPositive(out.Spacing, floatField(node, "itemSpacing"))
		recordIfPositive(out.Spacing, floatField(node, "counterAxisSpacing"))
		recordIfPositive(out.Padding, floatField(node, "paddingLeft"))
		recordIfPositive(out.Padding, floatField(node, "paddingRight"))
		recordIfPositive(out.Padding, floatField(node, "paddingTop"))
		recordIfPositive(out.Padding, floatField(node, "paddingBottom"))

		// cornerRadius is on FRAME/RECTANGLE; skip per-corner override arrays for now
		if cr, ok := node["cornerRadius"].(float64); ok && cr >= 0 {
			out.Radius[round1(cr)]++
		}
	}
	// RECTANGLE / VECTOR can also carry cornerRadius
	if t == "RECTANGLE" || t == "VECTOR" {
		if cr, ok := node["cornerRadius"].(float64); ok && cr > 0 {
			out.Radius[round1(cr)]++
		}
	}

	if children, ok := node["children"].([]any); ok {
		for _, child := range children {
			if cm, ok := child.(map[string]any); ok {
				walkLayout(cm, out)
			}
		}
	}
}

func recordIfPositive(h LayoutHistogram, v float64) {
	if v <= 0 {
		return
	}
	h[round1(v)]++
}

// round1 rounds to 1 decimal so 8.0000001 and 8 collapse into the same bucket.
func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
