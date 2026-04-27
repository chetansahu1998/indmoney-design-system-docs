package audit

import "strings"

// Ported from ~/DesignBrain-AI/internal/canvas/import_classify.go.
//
// LooksLikeScreen reports whether a frame's name suggests it's a final
// design page (vs. a WIP / sketches / playground). The heuristic is
// deliberately permissive — operators override per-file in
// lib/audit-files.json when names break convention.
func LooksLikeScreen(name string) bool {
	if len(name) < 3 {
		return false
	}
	lower := strings.ToLower(name)
	for _, kw := range screenKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

var screenKeywords = []string{"screen", "page", "view", "modal", "dialog", "sheet"}

// Kind enumerates how a node participates in the audit. Mirrors a subset
// of DesignBrain's ClassifyShapeSpecs categories.
type Kind string

const (
	KindScreen    Kind = "screen"
	KindSection   Kind = "section"
	KindFrame     Kind = "frame"
	KindComponent Kind = "component" // INSTANCE / COMPONENT / COMPONENT_SET
	KindText      Kind = "text"
	KindIcon      Kind = "icon"   // VECTOR / BOOL_OPERATION
	KindShape     Kind = "shape"  // RECTANGLE / ELLIPSE / IMAGE
	KindContainer Kind = "container" // GROUP
	KindOther     Kind = "other"
)

// ClassifyKind maps a Figma node `type` (and frame-name screen detection)
// to a high-level Kind. Mirrors DesignBrain's classifyShapeType.
func ClassifyKind(figmaType, name string) Kind {
	switch figmaType {
	case "SECTION":
		return KindSection
	case "FRAME":
		if LooksLikeScreen(name) {
			return KindScreen
		}
		return KindFrame
	case "INSTANCE", "COMPONENT", "COMPONENT_SET":
		return KindComponent
	case "TEXT":
		return KindText
	case "VECTOR", "BOOLEAN_OPERATION", "BOOL_OPERATION":
		return KindIcon
	case "RECTANGLE", "ELLIPSE", "IMAGE":
		return KindShape
	case "GROUP":
		return KindContainer
	}
	return KindOther
}
