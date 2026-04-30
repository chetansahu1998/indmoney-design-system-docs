// Package rules contains Phase 2 RuleRunner implementations and shared
// helpers used by the worker pool.
//
// treediff.go is a pure structural-diff helper: given two JSON-decoded
// canonical_tree blobs (map[string]any), return the list of structural
// differences as []Delta.
//
// Consumers:
//
//   - theme_parity.go (U2) — runs Diff between mode-resolved trees within a
//     screen and converts each Delta into a Violation row. Bound-only
//     divergences are filtered out by the runner BEFORE Diff using IgnoreKeys.
//   - cross_persona / component_governance (U3, U6) — re-uses Diff to compare
//     instance subtrees across personas / variants.
//
// Design notes:
//
//   - We model nodes as map[string]any to match the existing Phase 1 decode
//     pattern (see runner.go:147 decodeTree).
//   - Children arrays are diffed pair-wise by index. A length mismatch
//     produces a delta whose ObservedA/ObservedB contains "<missing>" on the
//     short side. Order-insensitive child matching is intentionally NOT done
//     here — Figma exports preserve sibling order and shuffling siblings is
//     itself a design-system signal worth flagging.
//   - DiffOpts.IgnoreKeys is a flat list applied at every depth. This is
//     enough for Phase 2's known set ("boundVariables",
//     "explicitVariableModes", "componentPropertyReferences"). If a future
//     consumer needs path-scoped ignores, add an IgnorePaths option without
//     breaking the IgnoreKeys contract.
package rules

import (
	"fmt"
	"strconv"
)

// Delta is one structural difference. Path is the slash-joinable breadcrumb
// from the root to the divergent property; Property is the key that differs;
// ObservedA/ObservedB are short human-readable string forms suitable for
// pasting into a Violation row's Observed column.
//
// Kind classifies the divergence so theme_parity (and any future consumer)
// can pick a Violation suggestion template:
//
//   - "type_mismatch"   — node-type or shape differs (RECTANGLE vs ELLIPSE).
//   - "name_divergence" — node name differs at the same path.
//   - "layout_drift"    — layout primitive differs (paddingLeft, width, ...).
//   - "visual_drift"    — visual primitive differs (fills, strokes, opacity).
type Delta struct {
	Path      []string
	Property  string
	ObservedA string
	ObservedB string
	Kind      string
}

// DiffOpts controls Diff behaviour.
//
// IgnoreKeys is a flat list of JSON keys the diff skips entirely at every
// depth — both as a property to compare and as a prefix to walk into.
type DiffOpts struct {
	IgnoreKeys []string
}

// Diff returns the structural differences between trees a and b. nil-safe:
// if either side is nil, returns zero deltas (the caller is responsible for
// distinguishing "tree absent" from "trees equal").
func Diff(a, b map[string]any, opts DiffOpts) []Delta {
	if a == nil || b == nil {
		return nil
	}
	ignore := make(map[string]struct{}, len(opts.IgnoreKeys))
	for _, k := range opts.IgnoreKeys {
		ignore[k] = struct{}{}
	}
	out := make([]Delta, 0)
	out = walkMap(a, b, nil, ignore, out)
	return out
}

// walkMap diffs two map nodes at the same path. The slice is appended to and
// returned to avoid the allocation of a parent collector type.
func walkMap(a, b map[string]any, path []string, ignore map[string]struct{}, out []Delta) []Delta {
	// Union of keys, deterministic-enough order: iterate a first, then any
	// extra keys present only in b. Map-iteration order in Go is randomized
	// per run, so we accept non-deterministic delta order — tests assert on
	// content, not order.
	seen := make(map[string]struct{}, len(a))
	for k, av := range a {
		if _, skip := ignore[k]; skip {
			seen[k] = struct{}{}
			continue
		}
		seen[k] = struct{}{}
		bv, ok := b[k]
		if !ok {
			out = append(out, Delta{
				Path:      append(append([]string{}, path...), k),
				Property:  k,
				ObservedA: shortValue(av),
				ObservedB: "<missing>",
				Kind:      kindFor(k),
			})
			continue
		}
		out = walkValue(av, bv, append(append([]string{}, path...), k), k, ignore, out)
	}
	for k, bv := range b {
		if _, skip := ignore[k]; skip {
			continue
		}
		if _, already := seen[k]; already {
			continue
		}
		out = append(out, Delta{
			Path:      append(append([]string{}, path...), k),
			Property:  k,
			ObservedA: "<missing>",
			ObservedB: shortValue(bv),
			Kind:      kindFor(k),
		})
	}
	return out
}

// walkValue dispatches on type. Mismatched types emit a single delta at the
// current property; equal scalar values are no-ops.
func walkValue(a, b any, path []string, property string, ignore map[string]struct{}, out []Delta) []Delta {
	// Type mismatch (object vs scalar, array vs object, ...).
	if !sameKind(a, b) {
		return append(out, Delta{
			Path:      path,
			Property:  property,
			ObservedA: shortValue(a),
			ObservedB: shortValue(b),
			Kind:      "type_mismatch",
		})
	}
	switch av := a.(type) {
	case map[string]any:
		bv := b.(map[string]any)
		return walkMap(av, bv, path, ignore, out)
	case []any:
		bv := b.([]any)
		return walkSlice(av, bv, path, ignore, out)
	default:
		// Scalar comparison.
		if !scalarEqual(a, b) {
			return append(out, Delta{
				Path:      path,
				Property:  property,
				ObservedA: shortValue(a),
				ObservedB: shortValue(b),
				Kind:      kindFor(property),
			})
		}
		return out
	}
}

// walkSlice diffs two arrays pair-wise by index. Length mismatch produces a
// delta per missing index on the short side.
func walkSlice(a, b []any, path []string, ignore map[string]struct{}, out []Delta) []Delta {
	la, lb := len(a), len(b)
	max := la
	if lb > max {
		max = lb
	}
	for i := 0; i < max; i++ {
		idxStr := strconv.Itoa(i)
		childPath := append(append([]string{}, path...), idxStr)
		switch {
		case i >= la:
			out = append(out, Delta{
				Path:      childPath,
				Property:  lastSegment(path),
				ObservedA: "<missing>",
				ObservedB: shortValue(b[i]),
				Kind:      "type_mismatch",
			})
		case i >= lb:
			out = append(out, Delta{
				Path:      childPath,
				Property:  lastSegment(path),
				ObservedA: shortValue(a[i]),
				ObservedB: "<missing>",
				Kind:      "type_mismatch",
			})
		default:
			out = walkValue(a[i], b[i], childPath, lastSegment(path), ignore, out)
		}
	}
	return out
}

// kindFor classifies a property name into a Delta.Kind. The mapping is
// intentionally conservative — anything not explicitly layout/visual lands
// under "type_mismatch" or "name_divergence" depending on the property.
func kindFor(property string) string {
	switch property {
	case "type":
		return "type_mismatch"
	case "name":
		return "name_divergence"
	}
	if isLayoutProperty(property) {
		return "layout_drift"
	}
	if isVisualProperty(property) {
		return "visual_drift"
	}
	return "visual_drift"
}

// isLayoutProperty returns true for layout primitives.
func isLayoutProperty(p string) bool {
	switch p {
	case "x", "y", "width", "height",
		"paddingLeft", "paddingRight", "paddingTop", "paddingBottom",
		"itemSpacing", "counterAxisSpacing",
		"layoutMode", "layoutWrap", "layoutAlign",
		"primaryAxisSizingMode", "counterAxisSizingMode",
		"primaryAxisAlignItems", "counterAxisAlignItems":
		return true
	}
	return false
}

// isVisualProperty returns true for visual primitives.
func isVisualProperty(p string) bool {
	switch p {
	case "fill", "fills", "stroke", "strokes",
		"opacity", "blendMode",
		"effects", "effect",
		"cornerRadius", "topLeftRadius", "topRightRadius",
		"bottomLeftRadius", "bottomRightRadius",
		"r", "g", "b", "a", "color":
		return true
	}
	return false
}

// sameKind reports whether two values share the same JSON-decoded "kind"
// (object, array, or scalar). nil counts as scalar.
func sameKind(a, b any) bool {
	_, am := a.(map[string]any)
	_, bm := b.(map[string]any)
	if am != bm {
		return false
	}
	_, as := a.([]any)
	_, bs := b.([]any)
	if as != bs {
		return false
	}
	return true
}

// scalarEqual compares two non-object, non-array values. JSON unmarshalling
// canonicalizes numbers to float64 and strings/bools straight, so == works
// for everything except nil-vs-missing (handled at the parent level).
func scalarEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// shortValue renders a value as a short human-readable string for use in
// Delta.ObservedA/B. Truncates objects/arrays to a small JSON-ish summary so
// downstream Violation rows stay legible.
func shortValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case map[string]any:
		// {k1:..., k2:..., ...} truncated to first 3 keys.
		s := "{"
		i := 0
		for k, vv := range x {
			if i > 0 {
				s += ","
			}
			s += k + ":" + shortValue(vv)
			i++
			if i >= 3 {
				if len(x) > 3 {
					s += ",..."
				}
				break
			}
		}
		return s + "}"
	case []any:
		return fmt.Sprintf("[%d items]", len(x))
	case float64:
		// Render integers without decimal noise.
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}

// lastSegment returns the last element of a path (or "" if empty). Used to
// derive a Property for slice-index deltas where the property is the parent
// array's key (e.g. "children").
func lastSegment(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}
