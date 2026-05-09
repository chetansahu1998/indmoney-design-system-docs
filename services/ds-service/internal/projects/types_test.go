package projects

import (
	"encoding/json"
	"testing"
)

// types_test.go pins the wire shape of POST /v1/projects/export against the
// JSON the Figma plugin actually emits (figma-plugin/ui.html — the
// `flows.push({...})` block plus the outer payload assembly). Pre-2026-05-09
// the server's ModeGroupPayload was group-shape (variable_collection_id +
// frames[]) while the plugin's emit was pair-shape (primary_frame_id +
// paired_frame_ids), so every plugin-driven export silently dropped its
// pair metadata into a zero-value struct. The drift was harmless because
// the server re-derives mode pairs from per-frame metadata, but it left
// the wire shape lying about what the server understood.
//
// The fixture below is a faithful minimal replica of the plugin's emit.
// If the plugin's payload shape changes, this test should be the first
// thing to fail.

const pluginPayloadFixture = `{
  "idempotency_key": "trace-abc-123",
  "file_id": "FILE-K",
  "file_name": "Plutus Onboarding",
  "flows": [{
    "section_id": "1:42",
    "frame_ids": ["100:1", "100:2", "100:3"],
    "frames": [
      {"frame_id":"100:1","x":0,"y":0,"width":375,"height":812,"name":"Home Light","variable_collection_id":"VC1","mode_id":"light","mode_label":"light","explicit_variable_modes_json":"{\"VC1\":\"light\"}"},
      {"frame_id":"100:2","x":0,"y":900,"width":375,"height":812,"name":"Home Dark","variable_collection_id":"VC1","mode_id":"dark","mode_label":"dark","explicit_variable_modes_json":"{\"VC1\":\"dark\"}"},
      {"frame_id":"100:3","x":400,"y":0,"width":375,"height":812,"name":"Solo","variable_collection_id":"","mode_id":"","mode_label":""}
    ],
    "platform": "mobile",
    "product": "Plutus",
    "path": "Onboarding/Home",
    "persona_name": "First-time user",
    "name": "Onboarding home",
    "mode_groups": [
      {"primary_frame_id":"100:1","paired_frame_ids":["100:2"],"modes":["light","dark"],"theme_parity_warning_at_export":false}
    ],
    "unpaired_frame_ids": ["100:3"]
  }]
}`

// TestExportRequest_DecodesPluginPayloadFully asserts every field the plugin
// emits round-trips into a non-zero Go value. A zero-value field for any
// slice / scalar coming straight off the plugin's JSON means the wire shape
// has drifted again and the test fixture above needs updating.
func TestExportRequest_DecodesPluginPayloadFully(t *testing.T) {
	var req ExportRequest
	if err := json.Unmarshal([]byte(pluginPayloadFixture), &req); err != nil {
		t.Fatalf("decode plugin payload: %v", err)
	}

	// Top-level
	if req.IdempotencyKey != "trace-abc-123" {
		t.Errorf("IdempotencyKey: got %q, want %q", req.IdempotencyKey, "trace-abc-123")
	}
	if req.FileID != "FILE-K" {
		t.Errorf("FileID: got %q, want FILE-K", req.FileID)
	}
	if req.FileName != "Plutus Onboarding" {
		t.Errorf("FileName: got %q", req.FileName)
	}
	if len(req.Flows) != 1 {
		t.Fatalf("Flows: got %d, want 1", len(req.Flows))
	}

	flow := req.Flows[0]
	if flow.SectionID == nil || *flow.SectionID != "1:42" {
		t.Errorf("SectionID: got %v, want 1:42", flow.SectionID)
	}
	if len(flow.FrameIDs) != 3 {
		t.Errorf("FrameIDs: got %d, want 3", len(flow.FrameIDs))
	}
	if len(flow.Frames) != 3 {
		t.Fatalf("Frames: got %d, want 3", len(flow.Frames))
	}

	// Per-frame: every plugin field must land in a non-zero slot for the
	// frames that have it set.
	f0 := flow.Frames[0]
	if f0.FrameID != "100:1" || f0.Width != 375 || f0.Height != 812 ||
		f0.Name != "Home Light" || f0.VariableCollectionID != "VC1" ||
		f0.ModeID != "light" || f0.ModeLabel != "light" ||
		f0.ExplicitVariableModesJSON == "" {
		t.Errorf("Frames[0]: %+v has zero-value field(s); plugin → server frame shape drifted", f0)
	}

	// ModeGroups — pair-shape (the 2026-05-09 alignment). Pre-fix this
	// would have decoded into a zero-value ModeGroupPayload.
	if len(flow.ModeGroups) != 1 {
		t.Fatalf("ModeGroups: got %d, want 1", len(flow.ModeGroups))
	}
	mg := flow.ModeGroups[0]
	if mg.PrimaryFrameID != "100:1" {
		t.Errorf("ModeGroup.PrimaryFrameID: got %q, want 100:1", mg.PrimaryFrameID)
	}
	if len(mg.PairedFrameIDs) != 1 || mg.PairedFrameIDs[0] != "100:2" {
		t.Errorf("ModeGroup.PairedFrameIDs: got %v, want [100:2]", mg.PairedFrameIDs)
	}
	if len(mg.Modes) != 2 || mg.Modes[0] != "light" || mg.Modes[1] != "dark" {
		t.Errorf("ModeGroup.Modes: got %v, want [light dark]", mg.Modes)
	}
	if mg.ThemeParityWarningAtExport {
		t.Errorf("ModeGroup.ThemeParityWarningAtExport: got true, want false")
	}

	// UnpairedFrameIDs — added 2026-05-09 to capture the third frame that
	// has no light/dark sibling.
	if len(flow.UnpairedFrameIDs) != 1 || flow.UnpairedFrameIDs[0] != "100:3" {
		t.Errorf("UnpairedFrameIDs: got %v, want [100:3]", flow.UnpairedFrameIDs)
	}
}

// TestExportRequest_ModeGroupPayload_RoundTrip asserts a marshal→unmarshal
// cycle of the canonical pair-shape produces an equivalent value. Catches
// JSON-tag typos and accidental field reordering that decoders tolerate but
// produce wrong-keyed output.
func TestExportRequest_ModeGroupPayload_RoundTrip(t *testing.T) {
	original := ModeGroupPayload{
		PrimaryFrameID:             "1:1",
		PairedFrameIDs:             []string{"1:2", "1:3"},
		Modes:                      []string{"light", "dark"},
		ThemeParityWarningAtExport: true,
	}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Spot-check: the wire keys must be the snake_case names the plugin
	// uses. A tag drift would break the plugin → server round-trip even
	// when the Go values look right in isolation.
	for _, want := range []string{
		`"primary_frame_id":"1:1"`,
		`"paired_frame_ids":["1:2","1:3"]`,
		`"modes":["light","dark"]`,
		`"theme_parity_warning_at_export":true`,
	} {
		if !contains(string(body), want) {
			t.Errorf("marshal output missing %q\nfull: %s", want, string(body))
		}
	}

	var roundtrip ModeGroupPayload
	if err := json.Unmarshal(body, &roundtrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundtrip.PrimaryFrameID != original.PrimaryFrameID ||
		!stringsEqual(roundtrip.PairedFrameIDs, original.PairedFrameIDs) ||
		!stringsEqual(roundtrip.Modes, original.Modes) ||
		roundtrip.ThemeParityWarningAtExport != original.ThemeParityWarningAtExport {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", roundtrip, original)
	}
}

// Local helpers: avoid pulling in strings/slices for one-off comparisons.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
