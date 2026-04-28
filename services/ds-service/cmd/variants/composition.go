// Composition walker for parent components.
//
// A parent (organism) is built from atoms. Figma represents this with
// INSTANCE nodes inside the parent's variant trees — each INSTANCE has a
// `componentId` pointing at the embedded component's id. To make the
// docs site honest about composition, we walk every parent variant tree
// at extraction time, collect every INSTANCE we find, and emit a
// CompositionRef per slot.
//
// Resolution (atom_slug ← component_id) is a separate post-pass — see
// resolveCompositions below. We split walk-then-resolve so the walker
// stays a pure tree traversal and resolution gets a single read across
// the full atom table after every parent has been walked.

package main

import "strings"

// walkVariantInstances flattens every INSTANCE inside a variant's
// children into a CompositionRef list. The variant root itself is a
// COMPONENT, not an INSTANCE — its children may contain INSTANCEs at
// any depth; we descend through frames/groups/components transparently.
//
// path is the slash-joined chain of ancestor names from the variant
// root down to (but not including) the current node, mirroring Figma's
// own a/b/c naming convention. Empty for top-level children.
func walkVariantInstances(variantNode map[string]any) []CompositionRef {
	if variantNode == nil {
		return nil
	}
	var out []CompositionRef
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		if node == nil {
			return
		}
		t, _ := node["type"].(string)
		name, _ := node["name"].(string)
		if t == "INSTANCE" {
			cid, _ := node["componentId"].(string)
			if cid != "" {
				out = append(out, CompositionRef{
					InstanceName: name,
					ComponentID:  cid,
					Path:         path,
				})
			}
			// Don't descend into instances — their inner tree belongs to
			// the embedded component, not the parent we're walking. The
			// INSTANCE itself is the slot; what's inside is somebody
			// else's responsibility to document.
			return
		}
		// FRAME / GROUP / COMPONENT / COMPONENT_SET / TEXT / RECTANGLE
		// etc. — recurse if the node has children.
		children, _ := node["children"].([]any)
		nextPath := path
		if name != "" {
			if nextPath == "" {
				nextPath = name
			} else {
				nextPath = nextPath + " / " + name
			}
		}
		for _, ch := range children {
			cm, _ := ch.(map[string]any)
			if cm == nil {
				continue
			}
			walk(cm, nextPath)
		}
	}
	// Seed walk from the variant's children, not the variant itself —
	// the variant IS the root frame; its name shouldn't appear in the
	// path of its descendants.
	children, _ := variantNode["children"].([]any)
	for _, ch := range children {
		cm, _ := ch.(map[string]any)
		if cm == nil {
			continue
		}
		walk(cm, "")
	}
	return out
}

// resolveCompositions fills AtomSlug + ResolvedTier + ResolvedName on
// every CompositionRef across every variant of every parent entry, by
// matching ComponentID against the manifest's full set of known nodes.
//
// Two indexes are built first:
//   - bySetID:     COMPONENT_SET id      → IconEntry
//   - byVariantID: COMPONENT (variant) id → (parent IconEntry, variant)
//
// An INSTANCE may point at either form; we look up bySetID first
// (parent embeds another COMPONENT_SET wholesale, e.g. embedding the
// canonical Bottom Nav), and fall back to byVariantID (parent embeds a
// specific variant of an atom). Refs that resolve to nothing keep
// AtomSlug empty — those are external library refs, deleted nodes,
// or Bottom Sheet molecules we don't yet stamp.
func resolveCompositions(m *Manifest) {
	bySetID := make(map[string]int, len(m.Icons))
	byVariantID := make(map[string]struct {
		entryIdx int
		variantIdx int
	}, len(m.Icons))
	for i, e := range m.Icons {
		if e.SetID != "" {
			bySetID[e.SetID] = i
		}
		for j, v := range e.Variants {
			if v.VariantID != "" {
				byVariantID[v.VariantID] = struct {
					entryIdx   int
					variantIdx int
				}{i, j}
			}
		}
	}
	for i := range m.Icons {
		entry := &m.Icons[i]
		for vi := range entry.Variants {
			v := &entry.Variants[vi]
			for ri := range v.Composes {
				ref := &v.Composes[ri]
				if idx, ok := bySetID[ref.ComponentID]; ok {
					ref.AtomSlug = m.Icons[idx].Slug
					ref.ResolvedTier = stringTier(m.Icons[idx].Tier)
					ref.ResolvedName = m.Icons[idx].Name
					continue
				}
				if hit, ok := byVariantID[ref.ComponentID]; ok {
					ref.AtomSlug = m.Icons[hit.entryIdx].Slug
					ref.ResolvedTier = stringTier(m.Icons[hit.entryIdx].Tier)
					ref.ResolvedName = m.Icons[hit.entryIdx].Name
				}
			}
		}
	}
}

// stringTier defends against IconEntry.Tier being absent on legacy
// manifest entries — empty means "untiered" (icon/logo/illustration)
// rather than a typo'd value.
func stringTier(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	return t
}
