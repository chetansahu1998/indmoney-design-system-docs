package projects

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildOrganismSignatures_Happy ensures that a synthetic manifest with one
// `kind=component` entry carrying composition_refs[] is reshaped into exactly
// one OrganismSignature with the expected sorted atom-slug set + sorted
// variant names.
func TestBuildOrganismSignatures_Happy(t *testing.T) {
	manifest := `{
		"icons": [
			{
				"slug": "list-on-surface",
				"name": "List on Surface",
				"kind": "component",
				"category": "Lists",
				"composition_refs": [
					{"atom_slug": "right-text"},
					{"atom_slug": "left-icon-default"},
					{"atom_slug": "right-icon"}
				],
				"variants": [
					{"name": "Left Icon=Yes, Right Icon=Yes, Right Text=Yes"},
					{"name": "Left Icon=No, Right Icon=Yes, Right Text=No"}
				]
			}
		]
	}`
	path := writeTempManifest(t, manifest)

	sigs, hash, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatalf("expected non-empty manifest hash")
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signature, got %d", len(sigs))
	}
	got := sigs[0]
	if got.Slug != "list-on-surface" {
		t.Errorf("Slug = %q; want list-on-surface", got.Slug)
	}
	if got.Category != "Lists" {
		t.Errorf("Category = %q; want Lists", got.Category)
	}
	// AtomSlugs must be sorted.
	wantAtoms := []string{"left-icon-default", "right-icon", "right-text"}
	if len(got.AtomSlugs) != len(wantAtoms) {
		t.Fatalf("AtomSlugs len = %d; want %d", len(got.AtomSlugs), len(wantAtoms))
	}
	for i, a := range wantAtoms {
		if got.AtomSlugs[i] != a {
			t.Errorf("AtomSlugs[%d] = %q; want %q", i, got.AtomSlugs[i], a)
		}
	}
	// VariantNames sorted alphabetically.
	if len(got.VariantNames) != 2 {
		t.Fatalf("VariantNames len = %d; want 2", len(got.VariantNames))
	}
	if got.VariantNames[0] >= got.VariantNames[1] {
		t.Errorf("variant names not sorted: %v", got.VariantNames)
	}
}

// TestBuildOrganismSignatures_SkipsNonComponents asserts that icon / logo /
// illustration entries are silently filtered out even when they carry refs.
func TestBuildOrganismSignatures_SkipsNonComponents(t *testing.T) {
	manifest := `{
		"icons": [
			{"slug": "arrow-up", "kind": "icon", "composition_refs": [{"atom_slug": "vector-up"}]},
			{"slug": "reliance-logo", "kind": "logo", "composition_refs": [{"atom_slug": "vector-r"}]},
			{"slug": "empty-state-1", "kind": "illustration", "composition_refs": [{"atom_slug": "vector-x"}]}
		]
	}`
	path := writeTempManifest(t, manifest)
	sigs, _, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 0 {
		t.Errorf("expected 0 signatures for non-component entries, got %d", len(sigs))
	}
}

// TestBuildOrganismSignatures_SkipsEmptyRefs ensures components without any
// non-empty atom_slug entries are filtered. A component whose refs are all
// blank or self-referential is unrepresentable as a fingerprint target.
func TestBuildOrganismSignatures_SkipsEmptyRefs(t *testing.T) {
	manifest := `{
		"icons": [
			{"slug": "no-refs", "kind": "component"},
			{"slug": "empty-refs", "kind": "component", "composition_refs": []},
			{"slug": "blank-atoms", "kind": "component", "composition_refs": [{"atom_slug": ""}]},
			{"slug": "self-ref", "kind": "component", "composition_refs": [{"atom_slug": "self-ref"}]}
		]
	}`
	path := writeTempManifest(t, manifest)
	sigs, _, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 0 {
		t.Errorf("expected 0 signatures when no usable refs exist, got %d (%v)", len(sigs), sigs)
	}
}

// TestBuildOrganismSignatures_Dedupe asserts duplicate atom_slug entries
// inside one component's composition_refs collapse to one entry in the
// signature's AtomSlugs.
func TestBuildOrganismSignatures_Dedupe(t *testing.T) {
	manifest := `{
		"icons": [
			{
				"slug": "list-row",
				"kind": "component",
				"composition_refs": [
					{"atom_slug": "right-text"},
					{"atom_slug": "right-text"},
					{"atom_slug": "left-icon-default"},
					{"atom_slug": "right-text"}
				]
			}
		]
	}`
	path := writeTempManifest(t, manifest)
	sigs, _, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 || len(sigs[0].AtomSlugs) != 2 {
		t.Fatalf("expected 1 signature with 2 deduped atoms; got %d signatures, atoms=%v",
			len(sigs), sigs[0].AtomSlugs)
	}
}

// TestBuildOrganismSignatures_MissingFile mirrors BuildComponentRows's
// `os.ErrNotExist → nil` contract. Dev environments without the manifest
// committed should not produce a fatal pipeline error.
func TestBuildOrganismSignatures_MissingFile(t *testing.T) {
	sigs, hash, err := BuildOrganismSignatures(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if sigs != nil {
		t.Errorf("expected nil signatures for missing file, got %v", sigs)
	}
	if hash != "" {
		t.Errorf("expected empty hash for missing file, got %q", hash)
	}
}

// TestBuildOrganismSignatures_MalformedJSON asserts a parse failure surfaces
// as an error (we don't want to silently swallow corrupt manifest data).
func TestBuildOrganismSignatures_MalformedJSON(t *testing.T) {
	path := writeTempManifest(t, `{this is not json`)
	_, _, err := BuildOrganismSignatures(path)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
}

// TestBuildOrganismSignatures_Deterministic confirms two runs over the same
// manifest produce identical hash + identical signature order (R3 requirement).
func TestBuildOrganismSignatures_Deterministic(t *testing.T) {
	manifest := `{
		"icons": [
			{"slug": "z-organism", "kind": "component", "composition_refs": [{"atom_slug": "atom-1"}, {"atom_slug": "atom-2"}]},
			{"slug": "a-organism", "kind": "component", "composition_refs": [{"atom_slug": "atom-3"}, {"atom_slug": "atom-4"}]}
		]
	}`
	path := writeTempManifest(t, manifest)
	a, hashA, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	b, hashB, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if hashA != hashB {
		t.Errorf("hash drift: %q vs %q", hashA, hashB)
	}
	if len(a) != len(b) {
		t.Fatalf("signature count drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Slug != b[i].Slug {
			t.Errorf("order drift at %d: %q vs %q", i, a[i].Slug, b[i].Slug)
		}
	}
	// Also confirm output is sorted alphabetically by slug.
	if a[0].Slug != "a-organism" || a[1].Slug != "z-organism" {
		t.Errorf("expected alphabetical order, got %v", []string{a[0].Slug, a[1].Slug})
	}
}

// TestBuildOrganismSignatures_VariantsComposes — when cmd/variants writes
// composition data into variants[].composes[] (the field shape the live
// pipeline actually uses), BuildOrganismSignatures should union the
// atom_slugs across all variants and emit one OrganismSignature with the
// merged set. Mirrors the real manifest layout post 2026-05-13 cmd/variants
// re-run.
func TestBuildOrganismSignatures_VariantsComposes(t *testing.T) {
	manifest := `{
		"icons": [
			{
				"slug": "list-on-surface",
				"name": "List on Surface",
				"kind": "component",
				"category": "Lists",
				"variants": [
					{
						"name": "Left Icon=Yes",
						"composes": [
							{"atom_slug": "left-icon-default"},
							{"atom_slug": "left-text"}
						]
					},
					{
						"name": "Left Icon=No, Right Text=Yes",
						"composes": [
							{"atom_slug": "left-text"},
							{"atom_slug": "right-text"}
						]
					}
				]
			}
		]
	}`
	path := writeTempManifest(t, manifest)
	sigs, _, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signature; got %d", len(sigs))
	}
	got := sigs[0]
	// Union across the two variants: left-icon-default + left-text + right-text
	want := []string{"left-icon-default", "left-text", "right-text"}
	if len(got.AtomSlugs) != len(want) {
		t.Fatalf("AtomSlugs len = %d; want %d (got %v)", len(got.AtomSlugs), len(want), got.AtomSlugs)
	}
	for i, w := range want {
		if got.AtomSlugs[i] != w {
			t.Errorf("AtomSlugs[%d] = %q; want %q", i, got.AtomSlugs[i], w)
		}
	}
}

// TestBuildOrganismSignatures_MixedSources — entries with refs in BOTH
// composition_refs (legacy) AND variants[].composes (current) merge into
// the same signature without duplication.
func TestBuildOrganismSignatures_MixedSources(t *testing.T) {
	manifest := `{
		"icons": [
			{
				"slug": "list",
				"kind": "component",
				"composition_refs": [{"atom_slug": "left-icon"}, {"atom_slug": "right-icon"}],
				"variants": [
					{"name": "v1", "composes": [{"atom_slug": "right-text"}, {"atom_slug": "left-icon"}]}
				]
			}
		]
	}`
	path := writeTempManifest(t, manifest)
	sigs, _, err := BuildOrganismSignatures(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sigs) != 1 || len(sigs[0].AtomSlugs) != 3 {
		t.Fatalf("expected 1 sig with 3 atoms (dedup); got %d sigs, atoms=%v", len(sigs), sigs[0].AtomSlugs)
	}
}

// TestOrganismSignature_AtomSlugSet rounds-trips AtomSlugs through the set
// helper and confirms membership semantics.
func TestOrganismSignature_AtomSlugSet(t *testing.T) {
	s := OrganismSignature{AtomSlugs: []string{"a", "b", "c"}}
	set := s.AtomSlugSet()
	if len(set) != 3 {
		t.Fatalf("expected 3 members, got %d", len(set))
	}
	if _, ok := set["b"]; !ok {
		t.Errorf("expected 'b' in set")
	}
	if _, ok := set["missing"]; ok {
		t.Errorf("expected 'missing' NOT in set")
	}
}

// writeTempManifest is a small helper that drops JSON to a temp file and
// returns the path. Test scope only.
func writeTempManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp manifest: %v", err)
	}
	return path
}
