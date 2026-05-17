package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// subflow.go — U1 of the MCP + PM authoring workflow plan
// (docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md).
//
// First-class entities for the {sub_product}/{sub_flow} taxonomy that
// already exists as parsed strings (figma_section_parser.go:41). Repo
// methods are tenant-scoped via *TenantRepo. Migration 0036 backs them.
//
// Lifecycle ordering — a sub_flow can exist BEFORE the Figma section:
//   1. PM creates a DRD; UpsertSubProduct + UpsertSubFlow run first.
//   2. Later, the designer creates a section named {SubProduct}/{SubFlow}
//      in Figma.
//   3. Autosync (U2) parses the section name, looks the sub_flow up by
//      name, and calls LinkSubFlowToFigmaSection to back-fill the
//      figma_section_id column.
//
// Name match contract: case-insensitive + whitespace-trimmed, mirroring
// the LOWER(TRIM(name)) unique index in migration 0036. Slug is the
// lowercased, hyphenated form of the name (see subFlowSlugify below).
//
// Cross-reference for U2: figma_section_parser.ParseSectionName returns
// the (sub_product, sub_flow) display names verbatim with TrimSpace +
// leading-fringe stripped. Passing those strings directly to
// UpsertSubProduct / UpsertSubFlow Just Works — both halves are
// normalised the same way (TrimSpace + LOWER) before lookup. The repo
// preserves the first-write casing in the `name` column for display.

// SubProduct is a first-class row in `sub_product`. One per top-level
// product taxonomy bucket (Wallet, INDstocks, Mutual Funds, ...).
type SubProduct struct {
	ID        string
	Name      string // preserved casing of the first writer
	Slug      string // lowercase, hyphenated; org-wide identifier
	CreatedAt time.Time
}

// SubFlow is a first-class row in `sub_flow`. Hangs off a SubProduct.
// drd_id and figma_section_id are nullable — the workflow allows
// authoring before the Figma section exists.
//
// Prototype columns (U3b — mig 0039) carry the KTD-8 "prototype as
// placeholder" lifecycle: a PM can attach an HTML prototype URL while
// the design is still in flight; autosync flips PrototypeSupersededAt
// the moment it links FigmaSectionID via a section on a final-classified
// page (figma_page_classifier.PageClassFinal, mig 0029).
type SubFlow struct {
	ID                    string
	SubProductID          string
	Name                  string
	Slug                  string  // lowercase, hyphenated; scoped to sub_product
	DRDID                 *string // nullable; set by U3 when a DRD is opened
	FigmaSectionID        *string // nullable; set by U2 autosync (gated by U3b on final-page residency)
	PrototypeURL          *string // nullable; set by AttachPrototype (U3b)
	PrototypeTitle        *string // nullable; companion to PrototypeURL
	PrototypeAttachedAt   *time.Time
	PrototypeSupersededAt *time.Time // nullable; set when a final-page section is linked
	CreatedAt             time.Time
}

// FullSlug returns the universal join key {sub_product.slug}/{sub_flow.slug}.
// This string is what every other system (Mixpanel, Storybook, Sentry,
// JIRA) keys off (KTD-6). Callers that have both rows in hand should
// prefer this helper over hand-concatenating, so the separator stays
// consistent if the convention ever shifts.
func (s SubFlow) FullSlug(subProduct SubProduct) string {
	return subProduct.Slug + "/" + s.Slug
}

// subFlowSlugify normalises a human-typed name into a URL-safe slug:
//
//	"M2M Settlement"       → "m2m-settlement"
//	"INDstocks F&O"        → "indstocks-f-o"
//	"  Wallet/Main Flow  " → "wallet-main-flow"
//
// Algorithm: lower-case, replace any non-[a-z0-9] run with a single
// hyphen, trim leading/trailing hyphens.
//
// Kept local to this file (not promoted to package scope) so it can't
// be confused with the existing makeSlug(product, path) helper in
// repository.go which has different semantics (project-level slugs).
func subFlowSlugify(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return out
}

// normalizeName trims and lowercases — the lookup key used against the
// LOWER(TRIM(name)) unique index in migration 0036.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// scanSubProduct is the shared row decoder.
func scanSubProduct(row interface {
	Scan(dest ...any) error
}) (SubProduct, error) {
	var sp SubProduct
	var createdAt string
	if err := row.Scan(&sp.ID, &sp.Name, &sp.Slug, &createdAt); err != nil {
		return SubProduct{}, err
	}
	sp.CreatedAt = parseTime(createdAt)
	return sp, nil
}

// subFlowSelectCols is the canonical column list for SELECT queries that
// build a SubFlow row. Centralised so the four prototype columns added
// by mig 0039 stay aligned across every reader. Order matches the
// Scan target list inside scanSubFlow.
const subFlowSelectCols = `id, sub_product_id, name, slug, drd_id, figma_section_id,
		prototype_url, prototype_title, prototype_attached_at, prototype_superseded_at,
		created_at`

// scanSubFlow is the shared row decoder.
func scanSubFlow(row interface {
	Scan(dest ...any) error
}) (SubFlow, error) {
	var sf SubFlow
	var createdAt string
	var drdID, figmaSectionID sql.NullString
	var protoURL, protoTitle, protoAttachedAt, protoSupersededAt sql.NullString
	if err := row.Scan(&sf.ID, &sf.SubProductID, &sf.Name, &sf.Slug,
		&drdID, &figmaSectionID,
		&protoURL, &protoTitle, &protoAttachedAt, &protoSupersededAt,
		&createdAt); err != nil {
		return SubFlow{}, err
	}
	if drdID.Valid {
		v := drdID.String
		sf.DRDID = &v
	}
	if figmaSectionID.Valid {
		v := figmaSectionID.String
		sf.FigmaSectionID = &v
	}
	if protoURL.Valid {
		v := protoURL.String
		sf.PrototypeURL = &v
	}
	if protoTitle.Valid {
		v := protoTitle.String
		sf.PrototypeTitle = &v
	}
	if protoAttachedAt.Valid {
		ts := parseTime(protoAttachedAt.String)
		sf.PrototypeAttachedAt = &ts
	}
	if protoSupersededAt.Valid {
		ts := parseTime(protoSupersededAt.String)
		sf.PrototypeSupersededAt = &ts
	}
	sf.CreatedAt = parseTime(createdAt)
	return sf, nil
}

// UpsertSubProduct returns the existing row for the given name (case-
// insensitive, trim) or creates one. Idempotent across concurrent
// callers via a re-read on UNIQUE collision.
//
// The first writer's casing is preserved in `name`; a subsequent
// UpsertSubProduct("WALLET") returns the original "Wallet" row.
func (t *TenantRepo) UpsertSubProduct(ctx context.Context, name string) (SubProduct, error) {
	if t.tenantID == "" {
		return SubProduct{}, errors.New("sub_product: tenant_id required")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return SubProduct{}, errors.New("sub_product: name required")
	}
	key := normalizeName(name)

	// Read first (read pool is fine — this is not a read-your-write path).
	existing, err := t.getSubProductByLowerName(ctx, key, t.readHandle())
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return SubProduct{}, err
	}

	// Insert. On UNIQUE collision (concurrent writer raced us), re-read
	// against the write handle for read-your-write consistency.
	id := uuid.NewString()
	slug := subFlowSlugify(trimmed)
	now := t.now().UTC()
	_, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO sub_product (id, tenant_id, name, slug, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, t.tenantID, trimmed, slug, rfc3339(now),
	)
	if ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getSubProductByLowerName(ctx, key, t.handle())
		}
		return SubProduct{}, fmt.Errorf("insert sub_product: %w", ierr)
	}
	return SubProduct{
		ID:        id,
		Name:      trimmed,
		Slug:      slug,
		CreatedAt: now,
	}, nil
}

// UpsertSubFlow returns the existing row for the given (subProductID,
// name) or creates one. Idempotent. drd_id and figma_section_id are
// left NULL — later units (U3, U2) back-fill them.
func (t *TenantRepo) UpsertSubFlow(ctx context.Context, subProductID, name string) (SubFlow, error) {
	if t.tenantID == "" {
		return SubFlow{}, errors.New("sub_flow: tenant_id required")
	}
	if subProductID == "" {
		return SubFlow{}, errors.New("sub_flow: sub_product_id required")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return SubFlow{}, errors.New("sub_flow: name required")
	}
	key := normalizeName(name)

	existing, err := t.getSubFlowByLowerName(ctx, subProductID, key, t.readHandle())
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return SubFlow{}, err
	}

	id := uuid.NewString()
	slug := subFlowSlugify(trimmed)
	now := t.now().UTC()
	_, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO sub_flow (id, tenant_id, sub_product_id, name, slug, drd_id, figma_section_id, created_at)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL, ?)`,
		id, t.tenantID, subProductID, trimmed, slug, rfc3339(now),
	)
	if ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getSubFlowByLowerName(ctx, subProductID, key, t.handle())
		}
		return SubFlow{}, fmt.Errorf("insert sub_flow: %w", ierr)
	}
	return SubFlow{
		ID:           id,
		SubProductID: subProductID,
		Name:         trimmed,
		Slug:         slug,
		CreatedAt:    now,
	}, nil
}

// GetSubProductByName returns the row whose LOWER(TRIM(name)) matches.
// Returns ErrNotFound on miss.
func (t *TenantRepo) GetSubProductByName(ctx context.Context, name string) (SubProduct, error) {
	if t.tenantID == "" {
		return SubProduct{}, errors.New("sub_product: tenant_id required")
	}
	return t.getSubProductByLowerName(ctx, normalizeName(name), t.readHandle())
}

func (t *TenantRepo) getSubProductByLowerName(ctx context.Context, lowerName string, h dbtx) (SubProduct, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, name, slug, created_at
		   FROM sub_product
		  WHERE tenant_id = ? AND LOWER(TRIM(name)) = ?`,
		t.tenantID, lowerName,
	)
	sp, err := scanSubProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubProduct{}, ErrNotFound
	}
	if err != nil {
		return SubProduct{}, fmt.Errorf("lookup sub_product: %w", err)
	}
	return sp, nil
}

// GetSubFlowByName returns the row scoped to (subProductID, name).
// Returns ErrNotFound on miss.
func (t *TenantRepo) GetSubFlowByName(ctx context.Context, subProductID, name string) (SubFlow, error) {
	if t.tenantID == "" {
		return SubFlow{}, errors.New("sub_flow: tenant_id required")
	}
	return t.getSubFlowByLowerName(ctx, subProductID, normalizeName(name), t.readHandle())
}

func (t *TenantRepo) getSubFlowByLowerName(ctx context.Context, subProductID, lowerName string, h dbtx) (SubFlow, error) {
	row := h.QueryRowContext(ctx,
		`SELECT `+subFlowSelectCols+`
		   FROM sub_flow
		  WHERE tenant_id = ? AND sub_product_id = ? AND LOWER(TRIM(name)) = ?`,
		t.tenantID, subProductID, lowerName,
	)
	sf, err := scanSubFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubFlow{}, ErrNotFound
	}
	if err != nil {
		return SubFlow{}, fmt.Errorf("lookup sub_flow: %w", err)
	}
	return sf, nil
}

// GetSubFlowBySlug resolves a sub_flow from its universal join key
// "{sub_product.slug}/{sub_flow.slug}" — e.g. "wallet/m2m-settlement".
// Returns ErrNotFound when either half misses or the slug is malformed.
func (t *TenantRepo) GetSubFlowBySlug(ctx context.Context, fullSlug string) (SubFlow, error) {
	if t.tenantID == "" {
		return SubFlow{}, errors.New("sub_flow: tenant_id required")
	}
	parts := strings.SplitN(fullSlug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return SubFlow{}, ErrNotFound
	}
	row := t.readHandle().QueryRowContext(ctx,
		`SELECT sf.id, sf.sub_product_id, sf.name, sf.slug, sf.drd_id, sf.figma_section_id,
		        sf.prototype_url, sf.prototype_title, sf.prototype_attached_at, sf.prototype_superseded_at,
		        sf.created_at
		   FROM sub_flow sf
		   JOIN sub_product sp
		     ON sp.tenant_id = sf.tenant_id AND sp.id = sf.sub_product_id
		  WHERE sf.tenant_id = ? AND sp.slug = ? AND sf.slug = ?`,
		t.tenantID, parts[0], parts[1],
	)
	sf, err := scanSubFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubFlow{}, ErrNotFound
	}
	if err != nil {
		return SubFlow{}, fmt.Errorf("lookup sub_flow by slug: %w", err)
	}
	return sf, nil
}

// GetSubFlowByFigmaSection returns the sub_flow bound to a Figma section
// id. Powers autosync's "section already known?" lookup (U2).
func (t *TenantRepo) GetSubFlowByFigmaSection(ctx context.Context, figmaSectionID string) (SubFlow, error) {
	if t.tenantID == "" {
		return SubFlow{}, errors.New("sub_flow: tenant_id required")
	}
	if figmaSectionID == "" {
		return SubFlow{}, ErrNotFound
	}
	row := t.readHandle().QueryRowContext(ctx,
		`SELECT `+subFlowSelectCols+`
		   FROM sub_flow
		  WHERE tenant_id = ? AND figma_section_id = ?`,
		t.tenantID, figmaSectionID,
	)
	sf, err := scanSubFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubFlow{}, ErrNotFound
	}
	if err != nil {
		return SubFlow{}, fmt.Errorf("lookup sub_flow by figma section: %w", err)
	}
	return sf, nil
}

// ListSubFlows returns every sub_flow for the tenant, optionally filtered
// by sub_product_id. Empty filter returns all. Ordered by created_at
// ascending — admin views show the oldest sub_flows first.
func (t *TenantRepo) ListSubFlows(ctx context.Context, subProductFilter string) ([]SubFlow, error) {
	if t.tenantID == "" {
		return nil, errors.New("sub_flow: tenant_id required")
	}
	var rows *sql.Rows
	var err error
	if subProductFilter == "" {
		rows, err = t.readHandle().QueryContext(ctx,
			`SELECT `+subFlowSelectCols+`
			   FROM sub_flow
			  WHERE tenant_id = ?
			  ORDER BY created_at ASC`,
			t.tenantID,
		)
	} else {
		rows, err = t.readHandle().QueryContext(ctx,
			`SELECT `+subFlowSelectCols+`
			   FROM sub_flow
			  WHERE tenant_id = ? AND sub_product_id = ?
			  ORDER BY created_at ASC`,
			t.tenantID, subProductFilter,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list sub_flow: %w", err)
	}
	defer rows.Close()
	var out []SubFlow
	for rows.Next() {
		sf, scanErr := scanSubFlow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan sub_flow: %w", scanErr)
		}
		out = append(out, sf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sub_flow: %w", err)
	}
	return out, nil
}

// LinkSubFlowToFigmaSection sets sub_flow.figma_section_id, the binding
// autosync (U2) writes when it first sees a Figma section whose parsed
// name matches an existing sub_flow row.
//
// The partial unique index `idx_sub_flow_figma_section` enforces a 1:1
// mapping per tenant — a second sub_flow trying to claim the same
// section id will surface a UNIQUE constraint error to the caller.
func (t *TenantRepo) LinkSubFlowToFigmaSection(ctx context.Context, subFlowID, figmaSectionID string) error {
	if t.tenantID == "" {
		return errors.New("sub_flow: tenant_id required")
	}
	if subFlowID == "" {
		return errors.New("sub_flow: id required")
	}
	if figmaSectionID == "" {
		return errors.New("sub_flow: figma_section_id required")
	}
	res, err := t.handle().ExecContext(ctx,
		`UPDATE sub_flow
		    SET figma_section_id = ?
		  WHERE tenant_id = ? AND id = ?`,
		figmaSectionID, t.tenantID, subFlowID,
	)
	if err != nil {
		return fmt.Errorf("link sub_flow figma section: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("link sub_flow rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
