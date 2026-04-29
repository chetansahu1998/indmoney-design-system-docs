package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
)

// RuleRunner is the abstraction the worker pool runs against each version.
//
// Phase 1 ships a single implementation (auditCoreRunner) that wraps the
// existing audit.Audit engine per-screen. Phase 2 will add ThemeParityRunner,
// CrossPersonaRunner, A11yRunner, and FlowGraphRunner — the WorkerPool will
// fan out across a slice of RuleRunners and merge the results before persist.
//
// Implementations MUST be stateless w.r.t. global state — token catalogs and
// component candidates flow in via constructors so tests can substitute fixtures
// without touching the filesystem.
type RuleRunner interface {
	// Run inspects every screen in v and returns the violations the engine
	// produced. Implementations should NOT write to the database; the worker
	// owns persistence so it can run a single DELETE-then-INSERT transaction.
	Run(ctx context.Context, v *ProjectVersion) ([]Violation, error)
}

// VersionScreenLoader is the subset of TenantRepo the auditCoreRunner reads
// from. Defined here so worker_test.go can supply an in-memory fake without
// pulling in a full TenantRepo.
type VersionScreenLoader interface {
	// LoadScreensWithTrees returns each screen in the version paired with its
	// canonical tree JSON (as produced by U4's pipeline). When the canonical
	// tree is absent (Phase 1 partial fixtures), an empty `{}` JSON string
	// stands in so the runner can still emit zero violations cleanly.
	LoadScreensWithTrees(ctx context.Context, versionID string) ([]ScreenWithTree, error)
}

// ScreenWithTree pairs a screens-row mirror with its canonical-tree blob.
type ScreenWithTree struct {
	ScreenID      string
	FlowID        string
	CanonicalTree string // JSON-encoded screen subtree
}

// ─── Phase 1: auditCoreRunner ────────────────────────────────────────────────

// auditCoreRunner wraps the existing audit.Audit engine per-screen, then maps
// each FixCandidate into a Violation row using the Phase 1 severity table.
//
// Token + component catalogs are injected at construction (not loaded inside
// Run) so the runner stays stateless and tests can pass empty slices to
// exercise zero-violation paths without filesystem dependency.
type auditCoreRunner struct {
	loader     VersionScreenLoader
	tokens     []audit.DSToken
	candidates []audit.DSCandidate
	opts       audit.Options
	severityFor func(rule string, fc audit.FixCandidate) string // override for Phase 2 tuning
}

// AuditCoreRunnerConfig collects the dependencies. Pass an empty Tokens or
// Candidates slice in tests; the engine treats empty catalogs as "nothing to
// match" and still returns coverage rows for every screen.
type AuditCoreRunnerConfig struct {
	Loader      VersionScreenLoader
	Tokens      []audit.DSToken
	Candidates  []audit.DSCandidate
	Options     audit.Options
	SeverityFor func(rule string, fc audit.FixCandidate) string
}

// NewAuditCoreRunner constructs the Phase 1 RuleRunner. SeverityFor defaults to
// MapPriorityToSeverity when nil — Phase 2's per-rule tuning can override it
// without forking the runner type.
func NewAuditCoreRunner(cfg AuditCoreRunnerConfig) RuleRunner {
	if cfg.SeverityFor == nil {
		cfg.SeverityFor = func(_ string, fc audit.FixCandidate) string {
			return MapPriorityToSeverity(fc)
		}
	}
	return &auditCoreRunner{
		loader:      cfg.Loader,
		tokens:      cfg.Tokens,
		candidates:  cfg.Candidates,
		opts:        cfg.Options,
		severityFor: cfg.SeverityFor,
	}
}

// Run implements RuleRunner. Loads screens, decodes each canonical tree, runs
// audit.Audit per-screen, then maps the resulting fixes into Violation rows.
//
// Errors short-circuit the run — the worker translates that into an
// audit_jobs row marked failed.
func (r *auditCoreRunner) Run(ctx context.Context, v *ProjectVersion) ([]Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("auditCoreRunner: nil version")
	}
	if r.loader == nil {
		return nil, fmt.Errorf("auditCoreRunner: loader not configured")
	}
	rows, err := r.loader.LoadScreensWithTrees(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("load screens: %w", err)
	}
	out := make([]Violation, 0)
	for _, sc := range rows {
		// Decode the canonical tree. An empty string is treated as "no tree
		// available" (Phase 1 fixtures may not have one) — engine returns zero
		// fixes for an empty document, so we end up with no violations for
		// that screen, which is exactly what we want.
		tree := decodeTree(sc.CanonicalTree)

		res := audit.Audit(tree, r.tokens, r.candidates, r.opts)
		// audit.Audit returns one AuditScreen per recognized FRAME inside the
		// document. For the Phase 1 RuleRunner shape we flatten every fix
		// from every screen into a Violation row attached to sc.ScreenID
		// (the DB-level screen id), preserving the audit-engine node info on
		// the row's property/observed/rule_id fields.
		for _, as := range res.Screens {
			for _, fc := range as.Fixes {
				out = append(out, Violation{
					ID:         uuid.NewString(),
					VersionID:  v.ID,
					ScreenID:   sc.ScreenID,
					TenantID:   v.TenantID,
					RuleID:     ruleIDFor(fc),
					Severity:   r.severityFor(ruleIDFor(fc), fc),
					Property:   fc.Property,
					Observed:   fc.Observed,
					Suggestion: suggestionFor(fc),
					Status:     "active",
				})
			}
		}
	}
	return out, nil
}

// decodeTree parses a canonical-tree JSON blob. Returns nil on parse failure
// so audit.Audit can emit zero violations cleanly rather than crashing. Empty
// strings are also nil-equivalent.
func decodeTree(s string) map[string]any {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// ruleIDFor derives a stable Phase 1 rule identifier from a FixCandidate. The
// Phase 1 audit core doesn't tag fixes with rule IDs, so we synthesize a short
// `<reason>.<property>` string. Phase 2's runners will set this explicitly.
func ruleIDFor(fc audit.FixCandidate) string {
	reason := fc.Reason
	if reason == "" {
		reason = "drift"
	}
	prop := fc.Property
	if prop == "" {
		prop = "unknown"
	}
	return reason + "." + prop
}

// suggestionFor renders a short action string. The audit core has rationale
// + token_path + replaced_by; the violations row only stores one string so we
// pick the most actionable one (token_path > replaced_by > rationale).
func suggestionFor(fc audit.FixCandidate) string {
	if fc.TokenPath != "" {
		return "Bind to " + fc.TokenPath
	}
	if fc.ReplacedBy != "" {
		return "Replace deprecated token with " + fc.ReplacedBy
	}
	return fc.Rationale
}

// ─── Default loader: *sql.DB → screens × canonical_trees ────────────────────

// dbVersionScreenLoader reads screens (via the screens table) joined to their
// canonical_tree (via screen_canonical_trees). Used in production wiring so the
// runner can stay stateless.
type dbVersionScreenLoader struct {
	db *sql.DB
}

// NewDBVersionScreenLoader constructs the production loader.
func NewDBVersionScreenLoader(db *sql.DB) VersionScreenLoader {
	return &dbVersionScreenLoader{db: db}
}

// LoadScreensWithTrees implements VersionScreenLoader.
func (l *dbVersionScreenLoader) LoadScreensWithTrees(ctx context.Context, versionID string) ([]ScreenWithTree, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, '')
		   FROM screens s
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ?
		  ORDER BY s.created_at ASC`,
		versionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScreenWithTree
	for rows.Next() {
		var sc ScreenWithTree
		if err := rows.Scan(&sc.ScreenID, &sc.FlowID, &sc.CanonicalTree); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ─── Phase 1 severity mapping table ──────────────────────────────────────────

// Severity values — mirrored on the violations.severity column constraint.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// MapPriorityToSeverity is the Phase 1 mapping table from the plan. Phase 2's
// per-rule severityFor() override hooks the same call site for fine-tuning
// without touching the engine.
//
//	Existing audit.FixCandidate.priority  →  violations.severity
//	P1 (deprecated tokens, theme breaks)  →  Critical
//	P1 (drift > threshold)                →  High
//	P2 (drift within threshold)           →  Medium
//	P3 (cosmetic, naming hygiene)         →  Low
//	P3 (info-grade, suggestions)          →  Info
//
// "deprecated" / "theme_break" reasons land at Critical regardless of priority.
// Within P3, "custom" component fixes are Low (actionable hygiene), while
// rationale-only suggestions are Info.
func MapPriorityToSeverity(fc audit.FixCandidate) string {
	switch fc.Priority {
	case audit.PriorityP1:
		switch fc.Reason {
		case "deprecated", "theme_break":
			return SeverityCritical
		}
		return SeverityHigh
	case audit.PriorityP2:
		return SeverityMedium
	case audit.PriorityP3:
		// "custom" / "unbound" fixes are actionable hygiene → Low.
		// Pure rationale-only suggestions (no token_path, no replaced_by) → Info.
		if fc.TokenPath != "" || fc.ReplacedBy != "" || fc.Reason == "custom" {
			return SeverityLow
		}
		return SeverityInfo
	}
	return SeverityInfo
}
