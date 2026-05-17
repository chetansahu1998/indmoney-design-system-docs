package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// subflow_prototype.go — U3b of the MCP + PM authoring workflow plan
// (docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md).
//
// KTD-8 "prototype as placeholder" lifecycle. A PM can attach an HTML
// prototype URL to a sub_flow while the design is still in flight. The
// viewer renders the URL in a sandboxed iframe in the canvas slot until
// autosync detects the bound Figma section sitting on a "final"-
// classified page (mig 0029 page_classification = 'final', wired by
// ClassifyPages during syncFileDeep). At that moment autosync links
// figma_section_id and stamps prototype_superseded_at — the viewer
// reacts to the SSE event and swaps the iframe for rendered Figma
// frames.
//
// The "design-shipped" gate lives in repository_figma_autosync_subflow.go
// (UpsertSubFlowFromSection — modified by this unit). This file owns the
// PM-facing attach/detach surface plus the lifecycle resolver every viewer
// route calls.

// ─── Errors ─────────────────────────────────────────────────────────────────

// ErrSubFlowNotFound is the sentinel AttachPrototype / DetachPrototype /
// CanvasLifecycle return when the supplied sub_flow_id isn't visible to
// the caller's tenant. The HTTP layer translates this to 404.
//
// Aliases ErrNotFound so callers can `errors.Is(err, ErrNotFound)` against
// the canonical projects sentinel; the more specific name is exported for
// MCP tool-error mapping (U6 will surface it as drd.attach_prototype's
// "sub_flow_not_found" code).
var ErrSubFlowNotFound = ErrNotFound

// ErrInvalidPrototypeURL is returned when the AttachPrototype URL fails
// validation: empty, > 2048 chars, or missing the https:// scheme. v1
// intentionally allows any HTTPS host (PM-hosted Figma proto, internal
// preview env, public sandbox); a future hardening pass can layer an
// allowlist on top.
var ErrInvalidPrototypeURL = errors.New("sub_flow: invalid prototype url")

// PrototypeURLMaxLength caps the column at 2048 chars — the practical
// browser URL limit + a small budget for query strings carrying a Figma
// share token. Stored as a constant so the MCP tool schema can mirror it.
const PrototypeURLMaxLength = 2048

// PrototypeTitleMaxLength caps the optional title at 200 chars — well
// over the viewer's display chip width, but far under SQLite's TEXT cap.
const PrototypeTitleMaxLength = 200

// ─── Event broker injection ────────────────────────────────────────────────

// SubFlowEventBroker is the narrow contract this file needs from the SSE
// layer. *sse.MemoryBroker satisfies it for free (Publish signature is
// identical). Defining a local interface — rather than importing the full
// sse.BrokerService — keeps TenantRepo decoupled from broker construction
// and lets tests pass a slice-capturing stub.
//
// Callers pass nil when they have no broker handy (CLIs, batch fixers);
// publish is then a no-op. The repo never holds a broker reference — the
// dependency is per-call.
type SubFlowEventBroker interface {
	Publish(channel string, event sse.Event)
}

// inboxChannelForTenant duplicates server.go's inboxBroadcastChannel
// helper as a file-local function — the projects package can't import
// itself, and lifting inboxBroadcastChannel out of server.go is a
// bigger refactor than this unit's scope. Keep the string format in
// sync with server.go:3134.
func inboxChannelForTenant(tenantID string) string {
	return "inbox:" + tenantID
}

// ─── Lifecycle resolver ────────────────────────────────────────────────────

// Lifecycle is the four-state KTD-8 canvas resolver — the viewer reads
// this string at mount + on every figma.design_shipped /
// drd.prototype_attached SSE tick to decide which renderer to mount.
type Lifecycle string

const (
	// LifecycleEmpty — neither a prototype URL nor a Figma section is
	// attached. The viewer renders the "attach a prototype or wait for
	// design" empty state.
	LifecycleEmpty Lifecycle = "empty"

	// LifecycleProtoOnly — a prototype URL is attached, no Figma section
	// bound, AND no in-flight WIP section parsed for the slug. The viewer
	// renders the prototype iframe full-bleed.
	LifecycleProtoOnly Lifecycle = "proto-only"

	// LifecycleProtoWIP — a prototype URL is attached AND a Figma section
	// has parsed to this sub_flow's slug, but the section currently lives
	// on a non-final page (the autosync gate held it back). The viewer
	// still renders the prototype but shows a "design in progress" hint
	// so the PM knows the designer has started.
	LifecycleProtoWIP Lifecycle = "proto-wip"

	// LifecycleDesignShipped — a Figma section is bound (autosync
	// detected the section on a final-classified page). The viewer
	// mounts the Figma frame renderer; the prototype URL stays on the
	// row for history but is no longer rendered.
	LifecycleDesignShipped Lifecycle = "design-shipped"
)

// CanvasLifecycle resolves the four-state KTD-8 enum for one sub_flow.
// Pure read; uses the read pool.
//
// Decision tree (matches the constants above):
//
//  1. figma_section_id IS NOT NULL                       → "design-shipped"
//  2. prototype_url    IS NULL                           → "empty"
//  3. Any figma_section row whose parsed (sub_product,
//     sub_flow) names match this sub_flow's name pair
//     AND whose page_classification != 'final'           → "proto-wip"
//  4. otherwise                                          → "proto-only"
//
// Step 3 reuses the override-then-parse precedence the autosync layer
// applies (admin overrides on figma_section.sub_product_override /
// sub_flow_override > ParseSectionName(figma_section.name)), so the
// WIP detection sees the same name pair autosync would see when it
// eventually binds.
//
// Returns ErrSubFlowNotFound when the sub_flow id isn't visible to the
// caller's tenant.
func (t *TenantRepo) CanvasLifecycle(ctx context.Context, subFlowID string) (Lifecycle, error) {
	if t.tenantID == "" {
		return "", errors.New("sub_flow: tenant_id required")
	}
	if subFlowID == "" {
		return "", ErrSubFlowNotFound
	}

	// Load the sub_flow + its sub_product name in one read. We need the
	// sub_product NAME (not just id) to compare against figma_section's
	// override / parsed-name pair for the WIP detection in step 3.
	var (
		hasSection     sql.NullString
		hasPrototype   sql.NullString
		subProductName string
		subFlowName    string
	)
	row := t.readHandle().QueryRowContext(ctx, `
		SELECT sf.figma_section_id, sf.prototype_url, sp.name, sf.name
		  FROM sub_flow sf
		  JOIN sub_product sp ON sp.tenant_id = sf.tenant_id AND sp.id = sf.sub_product_id
		 WHERE sf.tenant_id = ? AND sf.id = ?
	`, t.tenantID, subFlowID)
	if err := row.Scan(&hasSection, &hasPrototype, &subProductName, &subFlowName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSubFlowNotFound
		}
		return "", fmt.Errorf("load sub_flow for lifecycle: %w", err)
	}

	// Step 1 — Figma section bound wins absolutely.
	if hasSection.Valid && hasSection.String != "" {
		return LifecycleDesignShipped, nil
	}

	// Step 2 — no prototype attached.
	if !hasPrototype.Valid || hasPrototype.String == "" {
		return LifecycleEmpty, nil
	}

	// Step 3 — look for a WIP section. Match either admin override or
	// parser-derived name pair to the sub_flow's name, and require the
	// hosting page's classification to NOT be 'final'. We compare via
	// LOWER(TRIM(...)) to mirror the case-insensitive uniqueness the
	// sub_flow lookup index enforces.
	wipKey := normalizeName(subProductName) + "/" + normalizeName(subFlowName)
	var hasWIP int
	wipRow := t.readHandle().QueryRowContext(ctx, `
		SELECT 1
		  FROM figma_section fs
		  JOIN figma_page  fp
		    ON fp.tenant_id = fs.tenant_id
		   AND fp.file_key  = fs.file_key
		   AND fp.page_id   = fs.page_id
		 WHERE fs.tenant_id = ?
		   AND fs.deleted_at IS NULL
		   AND (
		         (TRIM(COALESCE(fs.sub_product_override,'')) <> ''
		          AND TRIM(COALESCE(fs.sub_flow_override,'')) <> ''
		          AND LOWER(TRIM(fs.sub_product_override)) || '/' || LOWER(TRIM(fs.sub_flow_override)) = ?)
		      OR (TRIM(COALESCE(fs.sub_product_override,'')) = ''
		          AND LOWER(TRIM(fs.name)) = ?)
		      OR (TRIM(COALESCE(fs.sub_flow_override,'')) = ''
		          AND LOWER(TRIM(fs.name)) = ?)
		       )
		   AND COALESCE(fp.page_classification,'') <> 'final'
		 LIMIT 1
	`, t.tenantID, wipKey, wipKey, wipKey)
	if err := wipRow.Scan(&hasWIP); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("scan wip section probe: %w", err)
	}
	if hasWIP == 1 {
		return LifecycleProtoWIP, nil
	}
	return LifecycleProtoOnly, nil
}

// ─── Attach / detach ───────────────────────────────────────────────────────

// AttachPrototype sets the prototype URL (and optional title) on a
// sub_flow. Idempotent — re-attaching the same URL is a no-op (no row
// change, no SSE event published).
//
// URL validation: HTTPS scheme required, length <= PrototypeURLMaxLength.
// Title is optional but capped at PrototypeTitleMaxLength.
//
// Publishes drd.prototype_attached on the tenant's inbox channel after
// a successful insert/update. broker may be nil — publish is then
// skipped (used by CLIs and the deattach-then-attach flow inside tests).
//
// Errors: ErrSubFlowNotFound, ErrInvalidPrototypeURL.
func (t *TenantRepo) AttachPrototype(ctx context.Context, subFlowID, url, title string, broker SubFlowEventBroker) error {
	if t.tenantID == "" {
		return errors.New("sub_flow: tenant_id required")
	}
	if subFlowID == "" {
		return ErrSubFlowNotFound
	}

	url = strings.TrimSpace(url)
	title = strings.TrimSpace(title)

	if err := validatePrototypeURL(url); err != nil {
		return err
	}
	if len(title) > PrototypeTitleMaxLength {
		return fmt.Errorf("%w: title > %d chars", ErrInvalidPrototypeURL, PrototypeTitleMaxLength)
	}

	// Read first (read-before-tx; the single-writer pool serialises).
	// We need the current row to (a) confirm tenant ownership before any
	// write, (b) skip the SSE publish when the URL is unchanged.
	current, err := t.loadSubFlowByID(ctx, subFlowID)
	if err != nil {
		return err
	}

	// Idempotent fast path: same URL + same title → no-op, no SSE.
	sameURL := current.PrototypeURL != nil && *current.PrototypeURL == url
	sameTitle := (title == "" && current.PrototypeTitle == nil) ||
		(current.PrototypeTitle != nil && *current.PrototypeTitle == title)
	if sameURL && sameTitle {
		return nil
	}

	now := t.now().UTC()
	var attachedAtArg any
	if current.PrototypeAttachedAt != nil {
		// Preserve the original attach timestamp across re-attachment —
		// PM intent first surfaced then, even if title is changing now.
		attachedAtArg = rfc3339(*current.PrototypeAttachedAt)
	} else {
		attachedAtArg = rfc3339(now)
	}
	var titleArg any
	if title == "" {
		titleArg = nil
	} else {
		titleArg = title
	}

	res, err := t.handle().ExecContext(ctx, `
		UPDATE sub_flow
		   SET prototype_url           = ?,
		       prototype_title         = ?,
		       prototype_attached_at   = ?
		 WHERE tenant_id = ? AND id = ?
	`, url, titleArg, attachedAtArg, t.tenantID, subFlowID)
	if err != nil {
		return fmt.Errorf("update sub_flow prototype: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update sub_flow prototype rows: %w", err)
	}
	if n == 0 {
		return ErrSubFlowNotFound
	}

	// Re-load to grab the universal-slug components for the SSE payload.
	if broker != nil {
		slug, slugErr := t.loadSubFlowFullSlug(ctx, subFlowID)
		if slugErr == nil {
			broker.Publish(inboxChannelForTenant(t.tenantID), sse.DRDPrototypeAttached{
				Tenant:       t.tenantID,
				SubFlowID:    subFlowID,
				SubFlowSlug:  slug,
				PrototypeURL: url,
				Title:        title,
			})
		}
		// A slug-resolution failure is non-fatal — the row write
		// succeeded and the SSE event is a best-effort notification.
	}
	return nil
}

// DetachPrototype clears prototype_url + prototype_title on a sub_flow.
// prototype_attached_at + prototype_superseded_at are preserved (history).
// No SSE event — explicit detach is rare; auto-detach happens via
// prototype_superseded_at when design ships.
//
// Returns nil (no-op) when the sub_flow already has no prototype URL.
func (t *TenantRepo) DetachPrototype(ctx context.Context, subFlowID string) error {
	if t.tenantID == "" {
		return errors.New("sub_flow: tenant_id required")
	}
	if subFlowID == "" {
		return ErrSubFlowNotFound
	}
	res, err := t.handle().ExecContext(ctx, `
		UPDATE sub_flow
		   SET prototype_url   = NULL,
		       prototype_title = NULL
		 WHERE tenant_id = ? AND id = ?
	`, t.tenantID, subFlowID)
	if err != nil {
		return fmt.Errorf("detach prototype: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("detach prototype rows: %w", err)
	}
	if n == 0 {
		return ErrSubFlowNotFound
	}
	return nil
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// validatePrototypeURL enforces the v1 contract: non-empty, https:// scheme,
// <= PrototypeURLMaxLength chars. v1 intentionally allows any HTTPS host.
func validatePrototypeURL(url string) error {
	if url == "" {
		return fmt.Errorf("%w: empty url", ErrInvalidPrototypeURL)
	}
	if len(url) > PrototypeURLMaxLength {
		return fmt.Errorf("%w: url > %d chars", ErrInvalidPrototypeURL, PrototypeURLMaxLength)
	}
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return fmt.Errorf("%w: must be https://", ErrInvalidPrototypeURL)
	}
	// Disallow whitespace in the URL (catches accidental newlines from
	// paste). Standard URLs never contain literal whitespace.
	if strings.ContainsAny(url, " \t\r\n") {
		return fmt.Errorf("%w: contains whitespace", ErrInvalidPrototypeURL)
	}
	return nil
}

// loadSubFlowByID is a tenant-scoped read of one row by primary key.
// Returns ErrSubFlowNotFound on miss. Used by AttachPrototype's
// read-before-write step and by the autosync gate.
func (t *TenantRepo) loadSubFlowByID(ctx context.Context, subFlowID string) (SubFlow, error) {
	row := t.handle().QueryRowContext(ctx,
		`SELECT `+subFlowSelectCols+`
		   FROM sub_flow
		  WHERE tenant_id = ? AND id = ?`,
		t.tenantID, subFlowID,
	)
	sf, err := scanSubFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubFlow{}, ErrSubFlowNotFound
	}
	if err != nil {
		return SubFlow{}, fmt.Errorf("load sub_flow %s: %w", subFlowID, err)
	}
	return sf, nil
}

// loadSubFlowFullSlug returns "{sub_product.slug}/{sub_flow.slug}" for a
// sub_flow id. Single query so the SSE publish path stays cheap.
func (t *TenantRepo) loadSubFlowFullSlug(ctx context.Context, subFlowID string) (string, error) {
	var spSlug, sfSlug string
	row := t.readHandle().QueryRowContext(ctx, `
		SELECT sp.slug, sf.slug
		  FROM sub_flow sf
		  JOIN sub_product sp ON sp.tenant_id = sf.tenant_id AND sp.id = sf.sub_product_id
		 WHERE sf.tenant_id = ? AND sf.id = ?
	`, t.tenantID, subFlowID)
	if err := row.Scan(&spSlug, &sfSlug); err != nil {
		return "", err
	}
	return spSlug + "/" + sfSlug, nil
}
