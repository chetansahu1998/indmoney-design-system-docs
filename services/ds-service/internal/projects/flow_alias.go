package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Phase 7 U5 — flow link aliasing on rename (Origin Q3).
//
// When a flow's path changes (designer moves it across the taxonomy),
// `projects.slug` is regenerated; we want prior URLs to keep working via a
// 301 redirect. flow_aliases is append-only — the live slug is always on
// projects.slug, this table just records every prior slug for that flow.

// RecordFlowAlias inserts a new alias row pointing oldSlug → newSlug for
// the given flow. Idempotent: if the same (tenant_id, slug) row already
// exists, ON CONFLICT updates redirected_to.
func (t *TenantRepo) RecordFlowAlias(ctx context.Context, flowID, oldSlug, newSlug string) error {
	if t.tenantID == "" {
		return errors.New("flow_alias: tenant_id required")
	}
	if oldSlug == "" || newSlug == "" || oldSlug == newSlug {
		return nil
	}
	_, err := t.r.db.ExecContext(ctx,
		`INSERT INTO flow_aliases (slug, tenant_id, flow_id, redirected_to, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, slug) DO UPDATE
		   SET redirected_to = excluded.redirected_to,
		       created_at = excluded.created_at`,
		oldSlug, t.tenantID, flowID, newSlug, rfc3339(t.now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("flow_alias insert: %w", err)
	}
	return nil
}

// LookupFlowAlias returns the live slug for a given alias, or "" when no
// alias exists. Used by HandleProjectGet when the requested slug doesn't
// match an active project — if an alias exists, the handler 301s.
func (t *TenantRepo) LookupFlowAlias(ctx context.Context, slug string) (string, error) {
	if t.tenantID == "" {
		return "", errors.New("flow_alias: tenant_id required")
	}
	var redirectedTo string
	err := t.r.db.QueryRowContext(ctx,
		`SELECT redirected_to FROM flow_aliases
		   WHERE tenant_id = ? AND slug = ?
		   ORDER BY created_at DESC LIMIT 1`,
		t.tenantID, slug,
	).Scan(&redirectedTo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("flow_alias lookup: %w", err)
	}
	return redirectedTo, nil
}
