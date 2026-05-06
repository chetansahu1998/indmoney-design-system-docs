package projects

// cluster_classifier_parity_test.go — fixture-driven parity test for U6
// of plan 2026-05-06-003. Loads testdata/cluster_classifier_fixture.json
// and asserts ExtractClusterIDs (Go) collects the expected node IDs for
// each case. The same fixture is consumed by the TS-side parity test in
// app/atlas/_lib/leafcanvas-v2/__tests__/node-classifier-parity.test.ts
// (landing in U7); drift on either side fails one of the two test runs.
//
// This is the structural-drift detector for the duplicate Go isCluster
// (services/ds-service/internal/projects/pipeline_cluster_prerender.go)
// and TS shouldRasterize (app/atlas/_lib/leafcanvas-v2/node-classifier.ts)
// implementations. Until classification moves entirely server-side
// (deferred per plan 2026-05-06-003 scope), this fixture is the contract
// between them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type classifierFixture struct {
	Cases []classifierCase `json:"cases"`
}

type classifierCase struct {
	Name               string      `json:"name"`
	Tree               interface{} `json:"tree"`
	ExpectedClusterIDs []string    `json:"expected_cluster_ids"`
	ComparisonNotes    string      `json:"comparison_notes,omitempty"`
}

func TestClusterClassifierParity_Fixture(t *testing.T) {
	path := filepath.Join("testdata", "cluster_classifier_fixture.json")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fix classifierFixture
	if err := json.Unmarshal(bs, &fix); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(fix.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}

	for _, c := range fix.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			treeJSON, err := json.Marshal(c.Tree)
			if err != nil {
				t.Fatalf("marshal tree: %v", err)
			}
			got := ExtractClusterIDs(treeJSON)

			gotSorted := append([]string(nil), got...)
			sort.Strings(gotSorted)
			wantSorted := append([]string(nil), c.ExpectedClusterIDs...)
			sort.Strings(wantSorted)

			if !stringSlicesEqual(gotSorted, wantSorted) {
				t.Fatalf("parity drift for case %q\n  want: %v\n  got:  %v\n  notes: %s",
					c.Name, wantSorted, gotSorted, c.ComparisonNotes)
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
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
