package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// repository_figma_autosync_subflow.go — U2 of the MCP + PM authoring
// workflow plan (docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md).
//
// Bridges the autosync hot path (poller.syncFileDeep) to the U1 sub_product
// + sub_flow tables. When a Figma section is observed, this method:
//
//  1. Reads any admin/Claude-supplied taxonomy overrides from figma_section
//     (sub_product_override / sub_flow_override — mig 0029).
//  2. Falls back to ParseSectionName(section.name) when overrides are
//     absent or blank. Slash-less names land under the "(unassigned)"
//     sub_product, matching the parser's existing bucket.
//  3. Upserts the sub_product + sub_flow rows (idempotent — U1 repo
//     returns the existing row on second call) and back-fills
//     sub_flow.figma_section_id so the binding is persisted.
//
// Precedence (mirrors autosync_planner.go:327 — keep these in sync):
//   admin override (both fields non-empty) > ParseSectionName.
//
// Tenant scoping: every query carries WHERE tenant_id = ? at the SQL site,
// per the codebase convention captured in the plan's Execution Notes §B.7.
//
// Read-before-tx: the override lookup hits the read handle first; the
// UpsertSubProduct / UpsertSubFlow / LinkSubFlowToFigmaSection calls
// underneath each open their own autocommit write — no nested tx is
// required because (a) U1's upserts are idempotent on UNIQUE collision
// and (b) the link write is a single UPDATE. The single-writer pool
// (db.go) serialises us anyway.

// UpsertSubFlowFromSection wires a Figma section into the sub_product /
// sub_flow taxonomy. Idempotent on re-runs (no duplicate rows, no FK
// churn) and tenant-scoped.
//
// Behaviour matrix:
//   - Section name "Wallet/M2M Settlement", no override → creates
//     sub_product="Wallet" + sub_flow="M2M Settlement" + links the
//     section id.
//   - Section name "Onboarding" (slash-less), no override → lands under
//     sub_product="(unassigned)" + sub_flow="Onboarding". The
//     "(unassigned)" bucket is materialised lazily on first encounter and
//     reused thereafter.
//   - sub_product_override + sub_flow_override BOTH non-empty → those
//     names win over ParseSectionName.
//   - Either override empty/blank → the override is treated as absent
//     (we don't half-apply).
//   - sub_flow row pre-exists from the DRD path with figma_section_id
//     NULL → fills figma_section_id on the existing row; no duplicate.
//   - Section renamed Wallet/Foo → Wallet/Bar: a NEW sub_flow row "Bar"
//     is created; the old "Foo" row stays (it represents PM intent that
//     may still hold) but loses the figma_section_id binding so the
//     partial unique index can re-grant it to the new row.
//   - Section name with different casing of the same words → matches the
//     existing row via LOWER(TRIM(name)) (U1's case-insensitive index).
//
// Returns the resulting SubFlow row (whether created or pre-existing).
func (t *TenantRepo) UpsertSubFlowFromSection(ctx context.Context, fileKey, pageID, sectionID, sectionName string) (SubFlow, error) {
	if t.tenantID == "" {
		return SubFlow{}, errors.New("sub_flow: tenant_id required")
	}
	if fileKey == "" || pageID == "" || sectionID == "" {
		return SubFlow{}, errors.New("sub_flow: file_key, page_id, section_id required")
	}

	// Read overrides from figma_section. Use the read handle — overrides
	// are admin-set well before autosync runs, and any staleness here just
	// falls through to ParseSectionName, which is the safe default.
	subProductName, subFlowName, err := t.pickSubFlowNames(ctx, fileKey, pageID, sectionID, sectionName)
	if err != nil {
		return SubFlow{}, err
	}

	// Upserts (idempotent — return existing row on second call).
	subProduct, err := t.UpsertSubProduct(ctx, subProductName)
	if err != nil {
		return SubFlow{}, fmt.Errorf("upsert sub_product %q: %w", subProductName, err)
	}
	subFlow, err := t.UpsertSubFlow(ctx, subProduct.ID, subFlowName)
	if err != nil {
		return SubFlow{}, fmt.Errorf("upsert sub_flow %q/%q: %w", subProductName, subFlowName, err)
	}

	// Bind sub_flow.figma_section_id. Fast path: already bound to the same
	// section id → nothing to do.
	if subFlow.FigmaSectionID != nil && *subFlow.FigmaSectionID == sectionID {
		return subFlow, nil
	}

	// The partial unique index (idx_sub_flow_figma_section) enforces 1:1
	// per tenant. If a DIFFERENT sub_flow currently owns this section id
	// (designer-renamed the section to map to a different sub_flow), clear
	// that binding first so this row can claim it. Match the rename
	// scenario from the plan: old row stays, just loses the section link.
	if err := t.clearStaleSubFlowSectionBinding(ctx, sectionID, subFlow.ID); err != nil {
		return SubFlow{}, fmt.Errorf("clear stale binding for section %s: %w", sectionID, err)
	}

	if err := t.LinkSubFlowToFigmaSection(ctx, subFlow.ID, sectionID); err != nil {
		return SubFlow{}, fmt.Errorf("link sub_flow %s -> section %s: %w", subFlow.ID, sectionID, err)
	}

	// Re-read so the returned struct carries the freshly-set
	// figma_section_id (test idempotency assertions rely on this).
	bound, err := t.GetSubFlowByFigmaSection(ctx, sectionID)
	if err != nil {
		return SubFlow{}, fmt.Errorf("re-read sub_flow after link: %w", err)
	}
	return bound, nil
}

// pickSubFlowNames computes the (sub_product, sub_flow) display names
// for a section, respecting admin overrides over ParseSectionName.
//
// Precedence: both override fields non-empty (after TrimSpace) → use
// them. Otherwise → ParseSectionName(sectionName). Mirrors the in-flight
// override read at autosync_planner.go:327.
func (t *TenantRepo) pickSubFlowNames(ctx context.Context, fileKey, pageID, sectionID, sectionName string) (subProduct, subFlow string, err error) {
	var subProductOverride, subFlowOverride sql.NullString
	row := t.readHandle().QueryRowContext(ctx, `
		SELECT sub_product_override, sub_flow_override
		  FROM figma_section
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND section_id = ?
		   AND deleted_at IS NULL
	`, t.tenantID, fileKey, pageID, sectionID)
	scanErr := row.Scan(&subProductOverride, &subFlowOverride)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return "", "", fmt.Errorf("read figma_section overrides: %w", scanErr)
	}
	// Missing row is tolerated — the caller may have just upserted the
	// section in a different connection; the override columns are NULL
	// in that case anyway. ParseSectionName carries the day.

	spOverride := ""
	if subProductOverride.Valid {
		spOverride = strings.TrimSpace(subProductOverride.String)
	}
	sfOverride := ""
	if subFlowOverride.Valid {
		sfOverride = strings.TrimSpace(subFlowOverride.String)
	}
	if spOverride != "" && sfOverride != "" {
		return spOverride, sfOverride, nil
	}

	subProduct, subFlow = ParseSectionName(sectionName)
	return subProduct, subFlow, nil
}

// clearStaleSubFlowSectionBinding drops figma_section_id on any sub_flow
// row that currently claims `sectionID` but is NOT `keepSubFlowID`. Used
// to handle the designer-rename case (test 7): old sub_flow keeps its
// row, just loses the section link so the new row can claim it under
// the partial unique index.
//
// No-op when no other row holds the binding — the common idempotent path.
func (t *TenantRepo) clearStaleSubFlowSectionBinding(ctx context.Context, sectionID, keepSubFlowID string) error {
	if sectionID == "" || keepSubFlowID == "" {
		return errors.New("sub_flow: section_id and keep_id required")
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE sub_flow
		   SET figma_section_id = NULL
		 WHERE tenant_id = ? AND figma_section_id = ? AND id <> ?
	`, t.tenantID, sectionID, keepSubFlowID)
	if err != nil {
		return fmt.Errorf("clear stale sub_flow figma_section_id: %w", err)
	}
	return nil
}

