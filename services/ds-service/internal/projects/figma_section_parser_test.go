package projects

import "testing"

func TestParseSectionName(t *testing.T) {
	cases := []struct {
		raw     string
		subProd string
		subFlow string
	}{
		// Convention examples from the plan.
		{"Wallet/Main Flow", "Wallet", "Main Flow"},
		{"Wallet/MTM handling", "Wallet", "MTM handling"},
		{"Wallet/Periodic Settlement", "Wallet", "Periodic Settlement"},

		// Multi-slash → first split wins; rest stays in sub_flow.
		{"Wallet/MTM/Settlement", "Wallet", "MTM/Settlement"},

		// Whitespace around the slash.
		{"  Wallet  /  Main Flow  ", "Wallet", "Main Flow"},

		// Leading emoji / decorative chars are stripped before split.
		{"🏁 Wallet/Main", "Wallet", "Main"},

		// Slash-less names → unassigned bucket, full name preserved as sub_flow.
		{"Hero", UnassignedSubProduct, "Hero"},
		{"SIP", UnassignedSubProduct, "SIP"},

		// Slash-less with leading whitespace.
		{"   Hero   ", UnassignedSubProduct, "Hero"},

		// Edge: empty input.
		{"", UnassignedSubProduct, ""},

		// Edge: leading slash → unassigned sub-product, sub_flow = the rest.
		{"/Main Flow", UnassignedSubProduct, "Main Flow"},

		// Real names from the existing corpus (slash-delimited but designers
		// used non-convention prefixes; we still split mechanically).
		{"s/research", "s", "research"},
		{"stock deets/sahaj", "stock deets", "sahaj"},
		{"SIP/Flash", "SIP", "Flash"},
		{"MTF Buy/Sell Case", "MTF Buy", "Sell Case"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			subProd, subFlow := ParseSectionName(tc.raw)
			if subProd != tc.subProd {
				t.Errorf("sub_product: got %q, want %q", subProd, tc.subProd)
			}
			if subFlow != tc.subFlow {
				t.Errorf("sub_flow: got %q, want %q", subFlow, tc.subFlow)
			}
		})
	}
}
