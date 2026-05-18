package mcp

import _ "embed"

// ConstitutionVersion is the schema-aware revision of the embedded
// constitution.md. Bump it whenever the markdown changes meaningfully
// (slug grammar, lifecycle states, error catalogue, typed-stems model).
// The corresponding test in constitution_test.go pins this constant so
// edits to constitution.md force a deliberate version bump rather than
// drifting silently.
const ConstitutionVersion = 3

//go:embed constitution.md
var constitutionMD string

// Constitution returns the server constitution that Claude reads as
// `initialize.serverInfo.instructions`. The leading line carries the
// version marker so a client can diff against `ConstitutionVersion`
// without re-parsing the markdown.
func Constitution() string {
	return constitutionMD
}
