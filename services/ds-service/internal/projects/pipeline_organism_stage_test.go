package projects

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// pipeline_organism_stage_test.go — Stage 6.7 integration tests for U5.
// These tests bypass the full pipeline.Run and drive runOrganismDetection
// directly so the test stays focused on Stage 6.7's contract: walk canonical
// trees, classify, persist verdicts via UpsertOrganismMatches.

// stage67Fixture extends orgTestFixture with the manifest path + the
// canonical tree fixtures loaded into the test screen.
type stage67Fixture struct {
	orgTestFixture
	pipeline *Pipeline
	canonicalTreesByScreen map[string]string // screenID → tree JSON
}

func newStage67Fixture(t *testing.T, fixtureNames ...string) stage67Fixture {
	t.Helper()
	base := seedOrgFixture(t)

	// Write a small manifest with one list-on-surface signature.
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifestBody := `{"icons":[
		{"slug":"list-on-surface","name":"List on Surface","kind":"component","category":"Lists",
		 "composition_refs":[{"atom_slug":"left-icon-default"},{"atom_slug":"right-icon"},{"atom_slug":"right-text"}],
		 "variants":[{"name":"Left Icon=Yes, Right Icon=Yes, Right Text=Yes"}]}
	]}`
	if err := os.WriteFile(manifestPath, []byte(manifestBody), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	pl := &Pipeline{
		Repo:         base.repo,
		ManifestPath: manifestPath,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	// Load fixtures and unwrap to the document level. The walker handles
	// {document:...} wrappers itself; we re-serialize to match what the
	// pipeline sees in `reattaches.treeJSON` (canonical tree wrapped in
	// {document: ...}).
	treesByScreen := map[string]string{}
	if len(fixtureNames) > 0 {
		fixturePath := filepath.Join("testdata", "organism_fixtures", fixtureNames[0]+".json")
		raw, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		var rawMap map[string]any
		if err := json.Unmarshal(raw, &rawMap); err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		// Extract the document body from the Figma API envelope.
		nodes, _ := rawMap["nodes"].(map[string]any)
		var doc map[string]any
		for _, v := range nodes {
			if entry, ok := v.(map[string]any); ok {
				if d, ok := entry["document"].(map[string]any); ok {
					doc = d
					break
				}
			}
		}
		if doc == nil {
			t.Fatalf("fixture %s missing document", fixtureNames[0])
		}
		// Wrap in a synthetic screen-root that the walker will skip past,
		// so the fixture FRAME itself is evaluated as a candidate.
		screenRoot := map[string]any{
			"id":   base.screenID,
			"name": "Phone screen",
			"type": "FRAME",
			"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 375.0, "height": 812.0},
			"children": []any{doc},
		}
		wrapped := map[string]any{"document": screenRoot}
		treeJSON, _ := json.Marshal(wrapped)
		treesByScreen[base.screenID] = string(treeJSON)
	}

	return stage67Fixture{orgTestFixture: base, pipeline: pl, canonicalTreesByScreen: treesByScreen}
}

// TestStage67_HappyPath — runs detection on a wild-tax-1 canonical, expects
// at least one detected_organism_match row.
func TestStage67_HappyPath(t *testing.T) {
	fx := newStage67Fixture(t, "wild-tax-1")
	screenIDs := []string{fx.screenID}
	trees := []string{fx.canonicalTreesByScreen[fx.screenID]}
	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)

	rows, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected ≥ 1 organism match row from wild-tax-1")
	}
	// The fixture has list-on-surface atoms (left-icon-default, right-text,
	// right-icon) so against our 1-entry catalog the top match should be
	// list-on-surface as exact or near.
	foundList := false
	for _, r := range rows {
		if r.SuspectedSlug == "list-on-surface" && (r.MatchKind == "exact" || r.MatchKind == "near") {
			foundList = true
			break
		}
	}
	if !foundList {
		t.Errorf("expected at least one exact|near match for list-on-surface; got rows: %+v", rows)
	}
}

// TestStage67_EmptyTreesNoOp — empty trees slice doesn't crash or write rows.
func TestStage67_EmptyTreesNoOp(t *testing.T) {
	fx := newStage67Fixture(t)
	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, nil, nil)
	rows, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows from empty input; got %d", len(rows))
	}
}

// TestStage67_MalformedTreeSkipped — one malformed canonical tree in a batch
// doesn't fail the stage; other screens still produce verdicts.
func TestStage67_MalformedTreeSkipped(t *testing.T) {
	fx := newStage67Fixture(t, "wild-tax-1")
	good := fx.canonicalTreesByScreen[fx.screenID]
	bad := "{not valid json"

	// Add a second screen for the bad tree so each has a distinct FK target.
	flowID, err := fx.repo.ListFlowsByProject(context.Background(), "")
	_ = flowID
	_ = err
	// Reuse the same screen — we just want to confirm the bad tree doesn't
	// crash the loop and the good one still produces rows.
	screenIDs := []string{fx.screenID, fx.screenID}
	trees := []string{bad, good}

	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)
	rows, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Error("expected ≥ 1 row from the good tree despite the malformed sibling")
	}
}

// TestStage67_Idempotent — running detection twice on the same input produces
// the same row count (UPSERT semantics, R3 requirement).
func TestStage67_Idempotent(t *testing.T) {
	fx := newStage67Fixture(t, "wild-tax-1")
	screenIDs := []string{fx.screenID}
	trees := []string{fx.canonicalTreesByScreen[fx.screenID]}

	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)
	first, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)
	second, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(first) != len(second) {
		t.Errorf("idempotency broken: first=%d second=%d", len(first), len(second))
	}
}

// TestStage67_TenantIsolation — detection writes rows under the pipeline
// repo's tenant only.
func TestStage67_TenantIsolation(t *testing.T) {
	fx := newStage67Fixture(t, "wild-tax-1")
	screenIDs := []string{fx.screenID}
	trees := []string{fx.canonicalTreesByScreen[fx.screenID]}
	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)

	// Tenant B sees no rows.
	others, err := fx.otherRepo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(others) != 0 {
		t.Errorf("tenant B leaked tenant A rows: %d", len(others))
	}
}

// TestStage67_EmptyManifestStillProducesRows — missing/empty manifest still
// runs detection. All verdicts come back as novel since the signature
// catalog is empty.
func TestStage67_EmptyManifestStillProducesRows(t *testing.T) {
	fx := newStage67Fixture(t, "wild-tax-1")
	fx.pipeline.ManifestPath = "/nonexistent/manifest.json"
	screenIDs := []string{fx.screenID}
	trees := []string{fx.canonicalTreesByScreen[fx.screenID]}
	fx.pipeline.runOrganismDetection(context.Background(), fx.versionID, screenIDs, trees)

	rows, err := fx.repo.ListOrganismMatchesForVersion(context.Background(), fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected verdicts even with empty manifest")
	}
	for _, r := range rows {
		if r.MatchKind != "novel" {
			t.Errorf("expected novel kind with empty catalog; got %q for frame %s", r.MatchKind, r.FrameID)
		}
		if r.SuspectedSlug != "" {
			t.Errorf("expected empty SuspectedSlug for novel; got %q", r.SuspectedSlug)
		}
	}
}

// TestStage67_NoRepo — nil Repo / nil Pipeline does not panic.
func TestStage67_NoRepo(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic when repo nil: %v", r)
		}
	}()
	var p *Pipeline
	p.runOrganismDetection(context.Background(), "vid", []string{"s"}, []string{`{}`})

	p2 := &Pipeline{} // Repo is nil
	p2.runOrganismDetection(context.Background(), "vid", []string{"s"}, []string{`{}`})
}
