// Package projects — Phase 3 U6 — audit-progress emitter shared between
// the worker (which wires to the SSE broker) and the rules registry's
// composite runner (which calls the emitter after each rule).
//
// Lives in this package (not in `rules/`) to avoid a circular import:
// `rules` already imports `projects` for the RuleRunner / Violation /
// ProjectVersion types, so the emitter type + context helpers must live
// at or below `projects`.

package projects

import "context"

// ProgressEmitter receives a per-rule-completion tick from the composite
// runner. Worker wires this to the SSE broker so the Violations tab can
// render audit-running progress without waiting for ProjectAuditComplete.
//
// completed is 1-indexed (the rule that just finished); total is the
// non-nil runner count. Implementations SHOULD apply their own
// throttling (typical: token-bucket at 100ms per channel) since rule
// completions arrive faster than the UI needs to repaint.
type ProgressEmitter func(completed, total int)

type progressKey struct{}

// WithProgress attaches a ProgressEmitter to the context. The composite
// runner in `internal/projects/rules` reads it via ProgressFromContext
// inside Run(). Pass nil to disable progress reporting (default).
func WithProgress(ctx context.Context, emit ProgressEmitter) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, emit)
}

// ProgressFromContext returns the emitter the worker attached, or nil
// when none is set (the runner skips emission cleanly).
func ProgressFromContext(ctx context.Context) ProgressEmitter {
	if fn, ok := ctx.Value(progressKey{}).(ProgressEmitter); ok {
		return fn
	}
	return nil
}
