// Package rules — Phase 2 RuleRunner registry + composite runner.
//
// `NewRegistry(db, tenantID, auditCore, fetcher)` returns a single
// `projects.RuleRunner` that fans out across every Phase 2 rule class plus
// the existing Phase 1 audit-core runner. The composite preserves the
// worker's contract — one Run() call → one []Violation slice — so Phase 1's
// worker.go integration doesn't change shape.
//
// Order matters when a rule errors mid-run: the composite short-circuits on
// the first error (intentional — partial audits would leave the violations
// table in a confusing state).
//
// Registry is per-tenant: loaders carry tenantID since Phase 1's TenantRepo
// pattern requires it. Build a Registry once per audit job using the job's
// tenant_id (NOT once per server boot).

package rules

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// NewRegistry builds the composite runner from raw deps.
//
// Production wiring (cmd/server/main.go) calls this once per claimed job
// inside the worker — but Phase 1's worker holds a single Runner pointer,
// so a per-tenant adapter is needed there. For Phase 2, the simpler shape
// is: one global registry built at boot using a pseudo-tenant + a
// tenant-aware composite that builds a per-tenant slice on each Run call.
//
// The TenantAwareRunner below does that wrapping. NewRegistry returns the
// composite directly for tests; production uses NewTenantAwareRunner.
func NewRegistry(db *sql.DB, tenantID string, auditCore projects.RuleRunner, fetcher PrototypeFetcher) projects.RuleRunner {
	if fetcher == nil {
		fetcher = NoopPrototypeFetcher()
	}
	runners := make([]projects.RuleRunner, 0, 7)
	if auditCore != nil {
		runners = append(runners, auditCore)
	}

	runners = append(runners,
		NewThemeParityRunner(ThemeParityConfig{
			Loader: NewDBResolvedTreeLoader(db, tenantID),
		}),
		NewCrossPersonaRunner(NewDBFlowsByProjectLoader(db, tenantID)),
		NewA11yContrast(A11yContrastConfig{
			Loader: NewDBScreenModeLoader(db, tenantID),
		}),
		NewA11yTouchTarget(A11yTouchTargetConfig{
			Loader: NewDBTouchTargetLoader(db, tenantID),
		}),
		NewFlowGraphRunner(
			NewDBFlowGraphLoader(db, tenantID),
			NewDBFlowGraphLinkStore(db, tenantID),
			fetcher,
		),
		NewComponentGovernanceRunner(NewDBScreensWithFlowsLoader(db, tenantID)),
	)

	return &compositeRunner{runners: runners}
}

// NewTenantAwareRunner wraps NewRegistry with a per-Run tenant resolution.
// The version passed to Run() carries TenantID; the wrapper builds a fresh
// composite for that tenant on each call. This lets the worker hold a single
// Runner pointer at boot — no per-job rewiring needed.
//
// auditCore is the Phase 1 runner; pass nil to skip its findings (tests).
func NewTenantAwareRunner(db *sql.DB, auditCore projects.RuleRunner, fetcher PrototypeFetcher) projects.RuleRunner {
	return &tenantAwareRunner{db: db, auditCore: auditCore, fetcher: fetcher}
}

// ─── Composite runner ───────────────────────────────────────────────────────

type compositeRunner struct {
	runners []projects.RuleRunner
}

func (c *compositeRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	var all []projects.Violation
	for i, r := range c.runners {
		if r == nil {
			continue
		}
		viols, err := r.Run(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("rule[%d]: %w", i, err)
		}
		all = append(all, viols...)
	}
	return all, nil
}

var _ projects.RuleRunner = (*compositeRunner)(nil)

// ─── Tenant-aware wrapper ───────────────────────────────────────────────────

// tenantAwareRunner builds a fresh per-tenant composite on each Run() call.
// Phase 1's worker holds one Runner pointer; this lets Phase 2 ship without
// touching the worker's lifecycle.
type tenantAwareRunner struct {
	db        *sql.DB
	auditCore projects.RuleRunner
	fetcher   PrototypeFetcher
}

func (t *tenantAwareRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("rules: nil version")
	}
	if v.TenantID == "" {
		return nil, fmt.Errorf("rules: version missing tenant_id")
	}
	registry := NewRegistry(t.db, v.TenantID, t.auditCore, t.fetcher)
	return registry.Run(ctx, v)
}

var _ projects.RuleRunner = (*tenantAwareRunner)(nil)

// ─── Flow-graph link store wrapping TenantRepo ──────────────────────────────

// dbFlowGraphLinkStore wraps TenantRepo.GetPrototypeLinks +
// UpsertPrototypeLinks for the flow_graph rule. Constructed per-tenant (the
// loaders carry tenantID alongside *sql.DB).
type dbFlowGraphLinkStore struct {
	db       *sql.DB
	tenantID string
}

// NewDBFlowGraphLinkStore returns a FlowGraphLinkStore backed by *sql.DB.
func NewDBFlowGraphLinkStore(db *sql.DB, tenantID string) FlowGraphLinkStore {
	return &dbFlowGraphLinkStore{db: db, tenantID: tenantID}
}

func (s *dbFlowGraphLinkStore) GetPrototypeLinks(ctx context.Context, versionID string) ([]projects.PrototypeLink, error) {
	return projects.NewTenantRepo(s.db, s.tenantID).GetPrototypeLinks(ctx, versionID)
}

func (s *dbFlowGraphLinkStore) UpsertPrototypeLinks(ctx context.Context, links []projects.PrototypeLink) error {
	return projects.NewTenantRepo(s.db, s.tenantID).UpsertPrototypeLinks(ctx, links)
}
