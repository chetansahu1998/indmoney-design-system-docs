package main

import "testing"

func mkRow(tab string, row int, fileID, nodeID, source string) NormalizedRow {
	return NormalizedRow{
		Tab: tab, RowIndex: row,
		FileID: fileID, NodeID: nodeID,
		URLKind: URLValid,
		Mapping: SubSheetMapping{Lobe: "markets", Product: tab, Source: source},
	}
}

func TestDedupe(t *testing.T) {
	t.Run("3 distinct rows, all pass through", func(t *testing.T) {
		in := []NormalizedRow{
			mkRow("INDstocks", 2, "F1", "1:1", "explicit"),
			mkRow("Plutus", 2, "F2", "2:2", "explicit"),
			mkRow("Insurance", 2, "F3", "3:3", "explicit"),
		}
		out, warns := Dedupe(in)
		if len(out) != 3 {
			t.Errorf("kept %d, want 3", len(out))
		}
		if len(warns) != 0 {
			t.Errorf("warnings = %v, want none", warns)
		}
	})

	t.Run("ghost rows + canvas-only pass through", func(t *testing.T) {
		in := []NormalizedRow{
			{Tab: "INDstocks", RowIndex: 2, URLKind: URLEmpty},
			{Tab: "INDstocks", RowIndex: 3, URLKind: URLCanvasOnly, FileID: "F1"},
			{Tab: "INDstocks", RowIndex: 4, URLKind: URLMalformed},
		}
		out, _ := Dedupe(in)
		if len(out) != 3 {
			t.Errorf("kept %d, want 3 (all unbucketed)", len(out))
		}
	})

	t.Run("same-tab duplicate keeps lowest row_index", func(t *testing.T) {
		in := []NormalizedRow{
			mkRow("INDstocks", 2, "F1", "1:1", "explicit"),
			mkRow("INDstocks", 5, "F1", "1:1", "explicit"),
			mkRow("INDstocks", 9, "F1", "1:1", "explicit"),
		}
		out, warns := Dedupe(in)
		if len(out) != 1 {
			t.Errorf("kept %d, want 1", len(out))
		}
		if out[0].RowIndex != 2 {
			t.Errorf("kept row %d, want 2 (lowest)", out[0].RowIndex)
		}
		if len(warns) != 2 {
			t.Errorf("warnings = %d, want 2 (one per dropped same-tab dup)", len(warns))
		}
	})

	t.Run("explicit beats default for same key", func(t *testing.T) {
		in := []NormalizedRow{
			mkRow("INDstocks", 2, "F1", "1:1", "explicit"),
			mkRow("Some Random Tab", 2, "F1", "1:1", "default"),
		}
		out, warns := Dedupe(in)
		if len(out) != 1 {
			t.Errorf("kept %d, want 1", len(out))
		}
		if out[0].Tab != "INDstocks" {
			t.Errorf("kept tab %q, want INDstocks", out[0].Tab)
		}
		if len(warns) != 1 {
			t.Errorf("warnings = %d, want 1 (default dropped)", len(warns))
		}
	})

	t.Run("two explicit tabs same key — keep both (legitimate cross-product)", func(t *testing.T) {
		in := []NormalizedRow{
			mkRow("INDstocks", 2, "F1", "1:1", "explicit"),
			mkRow("Plutus", 2, "F1", "1:1", "explicit"),
		}
		out, warns := Dedupe(in)
		if len(out) != 2 {
			t.Errorf("kept %d, want 2 (cross-product is legitimate)", len(out))
		}
		if len(warns) != 0 {
			t.Errorf("warnings = %v, want none", warns)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		out, warns := Dedupe(nil)
		if len(out) != 0 {
			t.Errorf("got %d, want 0", len(out))
		}
		if len(warns) != 0 {
			t.Errorf("warns = %d, want 0", len(warns))
		}
	})
}
