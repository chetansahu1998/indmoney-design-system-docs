package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
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
//     sub_product="Wallet" + sub_flow="M2M Settlement". figma_section_id
//     is linked ONLY if the hosting page is classified 'final' (mig 0029,
//     PageClassFinal).
//   - Section name "Onboarding" (slash-less), no override → lands under
//     sub_product="(unassigned)" + sub_flow="Onboarding". The
//     "(unassigned)" bucket is materialised lazily on first encounter and
//     reused thereafter.
//   - sub_product_override + sub_flow_override BOTH non-empty → those
//     names win over ParseSectionName.
//   - Either override empty/blank → the override is treated as absent
//     (we don't half-apply).
//   - sub_flow row pre-exists from the DRD path with figma_section_id
//     NULL → fills figma_section_id on the existing row when the page is
//     final-classified; otherwise leaves the binding NULL.
//   - Section renamed Wallet/Foo → Wallet/Bar: a NEW sub_flow row "Bar"
//     is created; the old "Foo" row stays (it represents PM intent that
//     may still hold) but loses the figma_section_id binding so the
//     partial unique index can re-grant it to the new row.
//   - Section name with different casing of the same words → matches the
//     existing row via LOWER(TRIM(name)) (U1's case-insensitive index).
//
// U3b "design-shipped" gate (KTD-8 — added 2026-05-17):
//
//	The figma_section_id flip is now conditional on the section sitting
//	on a final-classified figma_page (page_classification = 'final',
//	written by ClassifyPages during syncFileDeep — mig 0029). The
//	rationale: a designer's WIP page must not yank the prototype
//	iframe out from under the PM mid-authoring. Once the section
//	moves to (or appears directly on) a final page, the link flips
//	and:
//	  - prototype_superseded_at is stamped when prototype_url is set,
//	  - sse.FigmaDesignShipped is published on inbox:<tenant_id>.
//
//	The sub_flow row is still upserted (so PRD skeleton work in U2b
//	can run on WIP sections); only the binding is gated. The viewer's
//	"proto-wip" lifecycle state (CanvasLifecycle) surfaces the WIP
//	section to the PM without needing the binding.
//
//	The broker parameter may be nil — publish is then skipped. CLIs,
//	batch fixers, and tests that don't exercise the SSE path pass nil.
//
// Returns the resulting SubFlow row (whether created or pre-existing).
func (t *TenantRepo) UpsertSubFlowFromSection(ctx context.Context, fileKey, pageID, sectionID, sectionName string, broker SubFlowEventBroker) (SubFlow, error) {
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
	// section id → nothing to do. (Also short-circuits the SSE re-publish
	// path: if the binding survives, we never re-emit FigmaDesignShipped.)
	if subFlow.FigmaSectionID != nil && *subFlow.FigmaSectionID == sectionID {
		return subFlow, nil
	}

	// U3b — design-shipped gate (KTD-8). Only flip figma_section_id when
	// the section's hosting page is classified 'final' (mig 0029,
	// page_classification column written by ClassifyPages). On a WIP page
	// the sub_flow row stays usable (PRD skeleton can still build off it),
	// but the binding waits for the designer to move the section to a
	// final page.
	isFinal, classifyErr := t.isSectionPageFinal(ctx, fileKey, pageID)
	if classifyErr != nil {
		return SubFlow{}, fmt.Errorf("classify hosting page %s: %w", pageID, classifyErr)
	}
	if !isFinal {
		// Return the row as-is — figma_section_id still NULL (or holding
		// a previous binding from a former final page that since changed
		// — see edge case in plan §"Section moves off Final Designs").
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

	// If a prototype was attached to this sub_flow, stamp the supersede
	// timestamp now — the viewer reads this to know the iframe should
	// give way to Figma frames. Idempotent: an already-set value is
	// preserved by the partial WHERE clause.
	if subFlow.PrototypeURL != nil && *subFlow.PrototypeURL != "" && subFlow.PrototypeSupersededAt == nil {
		now := t.now().UTC()
		if _, err := t.handle().ExecContext(ctx, `
			UPDATE sub_flow
			   SET prototype_superseded_at = ?
			 WHERE tenant_id = ? AND id = ?
			   AND prototype_superseded_at IS NULL
		`, rfc3339(now), t.tenantID, subFlow.ID); err != nil {
			return SubFlow{}, fmt.Errorf("stamp prototype_superseded_at: %w", err)
		}
	}

	// Re-read so the returned struct carries the freshly-set
	// figma_section_id (test idempotency assertions rely on this).
	bound, err := t.GetSubFlowByFigmaSection(ctx, sectionID)
	if err != nil {
		return SubFlow{}, fmt.Errorf("re-read sub_flow after link: %w", err)
	}

	// Publish figma.design_shipped on the tenant inbox channel. Fires
	// once per transition (the fast-path early-return above prevents
	// re-emit on re-runs). Broker may be nil — production wires it from
	// poller.cfg.Broker; CLIs and tests pass nil.
	if broker != nil {
		slug, slugErr := t.loadSubFlowFullSlug(ctx, bound.ID)
		if slugErr == nil {
			broker.Publish(inboxChannelForTenant(t.tenantID), sse.FigmaDesignShipped{
				Tenant:         t.tenantID,
				SubFlowID:      bound.ID,
				SubFlowSlug:    slug,
				FigmaSectionID: sectionID,
			})
		}
		// Slug-resolution failure is non-fatal — the binding succeeded.
	}

	return bound, nil
}

// isSectionPageFinal reads figma_page.page_classification for one (file,
// page) pair and returns true when the value equals PageClassFinal. Any
// other value (including NULL / missing row) returns false — the safe
// default is "treat as WIP".
//
// Uses the write handle so a poller that just upserted the page in the
// same cycle observes the freshly-written classification (split-pool
// architecture — read pool can lag the writer by milliseconds).
func (t *TenantRepo) isSectionPageFinal(ctx context.Context, fileKey, pageID string) (bool, error) {
	var class sql.NullString
	row := t.handle().QueryRowContext(ctx, `
		SELECT page_classification
		  FROM figma_page
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ?
		   AND deleted_at IS NULL
	`, t.tenantID, fileKey, pageID)
	if err := row.Scan(&class); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !class.Valid {
		return false, nil
	}
	return PageClassification(class.String) == PageClassFinal, nil
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

