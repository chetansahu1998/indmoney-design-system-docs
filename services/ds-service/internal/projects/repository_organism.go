package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// repository_organism.go — TenantRepo methods for the organism-pattern-
// detection corpus introduced in migration 0024 (U1). Mirrors the
// repository.go conventions:
//
//   - Methods receive ctx + scalars + structs; never database/sql primitives.
//   - tenant_id is captured from TenantRepo and injected into every query —
//     callers can't forget the filter at the call site.
//   - Batch UPSERTs use prepared-statement-inside-tx (mirrors
//     UpsertPrototypeLinks at repository.go:1381).
//
// All methods are read- or write-side of the detected_organism_match +
// promotion_candidate tables. The compute primitives (walker / classifier)
// live in pipeline_organism_match.go; the wiring stage (U5) lives in
// pipeline.go.

// ─── detected_organism_match writes ──────────────────────────────────────────

// UpsertOrganismMatches writes the verdict rows for one project_version's
// detection pass. Idempotent on re-run: same fingerprints + same manifest =
// same rows. Primary key is (version_id, frame_id); UPSERT replaces existing
// rows so re-imports never accumulate orphans within a version.
//
// Empty slice = no-op (returns nil); the Stage 6.7 caller may skip the call
// to avoid an empty transaction.
func (t *TenantRepo) UpsertOrganismMatches(ctx context.Context, rows []DetectedOrganismMatch) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO detected_organism_match (
			version_id, frame_id, screen_id, tenant_id,
			suspected_slug, suspected_variant_key, match_kind, fingerprint_hash,
			atom_signature_json, slot_topology_json, diff_json, confidence,
			manifest_hash, parent_frame_id, detected_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(version_id, frame_id) DO UPDATE SET
			screen_id             = excluded.screen_id,
			suspected_slug        = excluded.suspected_slug,
			suspected_variant_key = excluded.suspected_variant_key,
			match_kind            = excluded.match_kind,
			fingerprint_hash      = excluded.fingerprint_hash,
			atom_signature_json   = excluded.atom_signature_json,
			slot_topology_json    = excluded.slot_topology_json,
			diff_json             = excluded.diff_json,
			confidence            = excluded.confidence,
			manifest_hash         = excluded.manifest_hash,
			parent_frame_id       = excluded.parent_frame_id,
			detected_at           = excluded.detected_at
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert detected_organism_match: %w", err)
	}
	defer stmt.Close()

	for i := range rows {
		r := &rows[i]
		if r.VersionID == "" || r.FrameID == "" || r.ScreenID == "" {
			return errors.New("projects: organism match missing version_id/frame_id/screen_id")
		}
		// Force the tenant_id so a caller's row.TenantID cannot leak across
		// tenants. Mirrors UpsertPrototypeLinks behavior.
		r.TenantID = t.tenantID

		// Nullable columns: SuspectedSlug, SuspectedVariantKey, DiffJSON,
		// ParentFrameID. Pass nil for empty strings so the DB stores SQL NULL.
		nullable := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}

		detectedAt := rfc3339(r.DetectedAt.UTC())
		if r.DetectedAt.IsZero() {
			detectedAt = rfc3339(t.now().UTC())
		}

		if _, err := stmt.ExecContext(ctx,
			r.VersionID, r.FrameID, r.ScreenID, t.tenantID,
			nullable(r.SuspectedSlug), nullable(r.SuspectedVariantKey),
			r.MatchKind, r.FingerprintHash,
			r.AtomSignatureJSON, r.SlotTopologyJSON, nullable(r.DiffJSON), r.Confidence,
			r.ManifestHash, nullable(r.ParentFrameID), detectedAt,
		); err != nil {
			return fmt.Errorf("upsert detected_organism_match (%s/%s): %w",
				r.VersionID, r.FrameID, err)
		}
	}

	return tx.Commit()
}

// ─── detected_organism_match reads ───────────────────────────────────────────

// LookupOrganismMatchByFrame returns the most-recent verdict for a given
// figma frame_id within the tenant. ErrNotFound when no row exists.
//
// Most-recent is determined by `detected_at DESC`. The plugin verdict endpoint
// (Part B U7) wants the latest verdict regardless of which version it came
// from — designers see the freshest signal for their currently-selected node.
func (t *TenantRepo) LookupOrganismMatchByFrame(ctx context.Context, frameID string) (DetectedOrganismMatch, error) {
	if t.tenantID == "" {
		return DetectedOrganismMatch{}, errors.New("projects: tenant_id required")
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT version_id, frame_id, screen_id, tenant_id,
		       COALESCE(suspected_slug, ''), COALESCE(suspected_variant_key, ''),
		       match_kind, fingerprint_hash,
		       atom_signature_json, slot_topology_json, COALESCE(diff_json, ''),
		       confidence, manifest_hash, COALESCE(parent_frame_id, ''),
		       detected_at
		FROM detected_organism_match
		WHERE tenant_id = ? AND frame_id = ?
		ORDER BY detected_at DESC
		LIMIT 1
	`, t.tenantID, frameID)

	var rec DetectedOrganismMatch
	var detectedAt string
	err := row.Scan(
		&rec.VersionID, &rec.FrameID, &rec.ScreenID, &rec.TenantID,
		&rec.SuspectedSlug, &rec.SuspectedVariantKey,
		&rec.MatchKind, &rec.FingerprintHash,
		&rec.AtomSignatureJSON, &rec.SlotTopologyJSON, &rec.DiffJSON,
		&rec.Confidence, &rec.ManifestHash, &rec.ParentFrameID,
		&detectedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DetectedOrganismMatch{}, ErrNotFound
	}
	if err != nil {
		return DetectedOrganismMatch{}, fmt.Errorf("lookup organism match: %w", err)
	}
	if rec.DetectedAt, err = time.Parse(time.RFC3339, detectedAt); err != nil {
		// Best-effort — surface the row even if the timestamp is non-canonical.
		rec.DetectedAt = time.Time{}
	}
	return rec, nil
}

// ListOrganismMatchesForVersion returns every verdict written for a project
// version in detected_at order. Used by Part A U5's pipeline pass for
// integration assertions and by Part D's clustering aggregator.
func (t *TenantRepo) ListOrganismMatchesForVersion(ctx context.Context, versionID string) ([]DetectedOrganismMatch, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT version_id, frame_id, screen_id, tenant_id,
		       COALESCE(suspected_slug, ''), COALESCE(suspected_variant_key, ''),
		       match_kind, fingerprint_hash,
		       atom_signature_json, slot_topology_json, COALESCE(diff_json, ''),
		       confidence, manifest_hash, COALESCE(parent_frame_id, ''),
		       detected_at
		FROM detected_organism_match
		WHERE tenant_id = ? AND version_id = ?
		ORDER BY detected_at DESC, frame_id ASC
	`, t.tenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf("list organism matches: %w", err)
	}
	defer rows.Close()

	out := make([]DetectedOrganismMatch, 0)
	for rows.Next() {
		var rec DetectedOrganismMatch
		var detectedAt string
		if err := rows.Scan(
			&rec.VersionID, &rec.FrameID, &rec.ScreenID, &rec.TenantID,
			&rec.SuspectedSlug, &rec.SuspectedVariantKey,
			&rec.MatchKind, &rec.FingerprintHash,
			&rec.AtomSignatureJSON, &rec.SlotTopologyJSON, &rec.DiffJSON,
			&rec.Confidence, &rec.ManifestHash, &rec.ParentFrameID,
			&detectedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organism match: %w", err)
		}
		if rec.DetectedAt, err = time.Parse(time.RFC3339, detectedAt); err != nil {
			rec.DetectedAt = time.Time{}
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountOrganismMatchesByKind returns COUNT(*) grouped by match_kind for the
// tenant. Powers Part C U11's adoption table (one row per kind per organism
// — the slug-broken-down version is CountOrganismMatchesBySlugAndKind).
func (t *TenantRepo) CountOrganismMatchesByKind(ctx context.Context) (map[string]int, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT match_kind, COUNT(*) AS n
		FROM detected_organism_match
		WHERE tenant_id = ?
		GROUP BY match_kind
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("count organism matches: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, fmt.Errorf("scan kind count: %w", err)
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// ListOrganismMatchesBySlug returns paginated matches for one published slug
// + match_kind. Powers Part C U11's drill-in view.
//
// Pass kind = "" to include every match_kind. limit ≤ 0 defaults to 100.
func (t *TenantRepo) ListOrganismMatchesBySlug(ctx context.Context, slug, kind string, limit, offset int) ([]DetectedOrganismMatch, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var sb strings.Builder
	args := []any{t.tenantID, slug}
	// COALESCE so empty-string slug matches both "" and NULL suspected_slug
	// (the "(unmatched-novel)" bucket on the admin dashboard).
	sb.WriteString(`
		SELECT version_id, frame_id, screen_id, tenant_id,
		       COALESCE(suspected_slug, ''), COALESCE(suspected_variant_key, ''),
		       match_kind, fingerprint_hash,
		       atom_signature_json, slot_topology_json, COALESCE(diff_json, ''),
		       confidence, manifest_hash, COALESCE(parent_frame_id, ''),
		       detected_at
		FROM detected_organism_match
		WHERE tenant_id = ? AND COALESCE(suspected_slug, '') = ?
	`)
	if kind != "" {
		sb.WriteString(" AND match_kind = ?")
		args = append(args, kind)
	}
	sb.WriteString(" ORDER BY detected_at DESC, frame_id ASC LIMIT ? OFFSET ?")
	args = append(args, limit, offset)

	rows, err := t.r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list organism matches by slug: %w", err)
	}
	defer rows.Close()

	out := make([]DetectedOrganismMatch, 0)
	for rows.Next() {
		var rec DetectedOrganismMatch
		var detectedAt string
		if err := rows.Scan(
			&rec.VersionID, &rec.FrameID, &rec.ScreenID, &rec.TenantID,
			&rec.SuspectedSlug, &rec.SuspectedVariantKey,
			&rec.MatchKind, &rec.FingerprintHash,
			&rec.AtomSignatureJSON, &rec.SlotTopologyJSON, &rec.DiffJSON,
			&rec.Confidence, &rec.ManifestHash, &rec.ParentFrameID,
			&detectedAt,
		); err != nil {
			return nil, fmt.Errorf("scan organism match: %w", err)
		}
		if rec.DetectedAt, err = time.Parse(time.RFC3339, detectedAt); err != nil {
			rec.DetectedAt = time.Time{}
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ─── Adoption rollup (U10 — dashboard ingredient) ────────────────────────────

// OrganismAdoptionRow is one slug's adoption snapshot for the admin
// dashboard. Slug == "" means the row represents the "(unmatched-novel)"
// bucket — patterns that didn't match any published organism.
type OrganismAdoptionRow struct {
	Slug          string `json:"slug"`
	Name          string `json:"name,omitempty"`
	Category      string `json:"category,omitempty"`
	InstanceCount int    `json:"instance_count"` // placeholder — populated once graph_index uses-edges are wired
	Exact         int    `json:"exact"`
	Near          int    `json:"near"`
	Novel         int    `json:"novel"`
}

// OrganismAdoptionRollup returns one OrganismAdoptionRow per
// distinct suspected_slug (incl. the empty bucket as "(unmatched)").
// Powers U11's adoption table. Sorted with empty-slug last so the
// "real" rows render at the top of the table.
func (t *TenantRepo) OrganismAdoptionRollup(ctx context.Context) ([]OrganismAdoptionRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT
		  COALESCE(suspected_slug, '') AS slug,
		  SUM(CASE WHEN match_kind = 'exact' THEN 1 ELSE 0 END) AS exact_count,
		  SUM(CASE WHEN match_kind = 'near'  THEN 1 ELSE 0 END) AS near_count,
		  SUM(CASE WHEN match_kind = 'novel' THEN 1 ELSE 0 END) AS novel_count
		FROM detected_organism_match
		WHERE tenant_id = ?
		GROUP BY COALESCE(suspected_slug, '')
		ORDER BY CASE WHEN COALESCE(suspected_slug,'') = '' THEN 1 ELSE 0 END, slug
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("adoption rollup: %w", err)
	}
	defer rows.Close()

	out := make([]OrganismAdoptionRow, 0)
	for rows.Next() {
		var r OrganismAdoptionRow
		if err := rows.Scan(&r.Slug, &r.Exact, &r.Near, &r.Novel); err != nil {
			return nil, fmt.Errorf("scan adoption row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─── promotion_candidate writes + reads ──────────────────────────────────────

// UpsertPromotionCandidates writes the full set of promotion candidates for a
// tenant. Replace-set semantics scoped by tenant:
//
//  1. DELETE every existing candidate for the tenant.
//  2. INSERT the new set.
//
// Mirrors UpsertPrototypeLinks. Re-running Part D's aggregation produces the
// authoritative current set; dropped candidates auto-remove. Re-aggregation
// runs after each Stage 6.7 pass (U5).
//
// Empty slice IS a meaningful payload — it indicates the tenant currently has
// zero clusters meeting the K/N thresholds, and the table should reflect that
// by becoming empty. Caller passes an explicit zero-length slice to clear.
func (t *TenantRepo) UpsertPromotionCandidates(ctx context.Context, rows []PromotionCandidate) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}

	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM promotion_candidate WHERE tenant_id = ?`, t.tenantID,
	); err != nil {
		return fmt.Errorf("delete prior promotion_candidates: %w", err)
	}

	if len(rows) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO promotion_candidate (
			tenant_id, fingerprint_hash, frequency, file_count,
			stability_score, atom_reuse_rate,
			proposed_name, dismissed_at, first_seen, last_seen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert promotion_candidate: %w", err)
	}
	defer stmt.Close()

	for i := range rows {
		r := &rows[i]
		if r.FingerprintHash == "" {
			return errors.New("projects: promotion_candidate missing fingerprint_hash")
		}
		r.TenantID = t.tenantID

		nullable := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}
		var dismissedAt any
		if !r.DismissedAt.IsZero() {
			dismissedAt = rfc3339(r.DismissedAt.UTC())
		}
		firstSeen := rfc3339(r.FirstSeen.UTC())
		if r.FirstSeen.IsZero() {
			firstSeen = rfc3339(t.now().UTC())
		}
		lastSeen := rfc3339(r.LastSeen.UTC())
		if r.LastSeen.IsZero() {
			lastSeen = firstSeen
		}

		if _, err := stmt.ExecContext(ctx,
			t.tenantID, r.FingerprintHash, r.Frequency, r.FileCount,
			r.StabilityScore, r.AtomReuseRate,
			nullable(r.ProposedName), dismissedAt, firstSeen, lastSeen,
		); err != nil {
			return fmt.Errorf("insert promotion_candidate (%s): %w", r.FingerprintHash, err)
		}
	}

	return tx.Commit()
}

// ListPromotionCandidates returns the tenant's candidates ranked by
// (frequency * stability_score * atom_reuse_rate) DESC, omitting dismissed
// rows. Powers Part C U14's promotion panel.
//
// limit ≤ 0 defaults to 20 (the panel's top-N view).
func (t *TenantRepo) ListPromotionCandidates(ctx context.Context, limit int) ([]PromotionCandidate, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT tenant_id, fingerprint_hash, frequency, file_count,
		       stability_score, atom_reuse_rate,
		       COALESCE(proposed_name, ''), COALESCE(dismissed_at, ''),
		       first_seen, last_seen
		FROM promotion_candidate
		WHERE tenant_id = ? AND dismissed_at IS NULL
		ORDER BY (CAST(frequency AS REAL) * stability_score * atom_reuse_rate) DESC,
		         fingerprint_hash ASC
		LIMIT ?
	`, t.tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list promotion candidates: %w", err)
	}
	defer rows.Close()

	out := make([]PromotionCandidate, 0)
	for rows.Next() {
		var rec PromotionCandidate
		var dismissedAt, firstSeen, lastSeen string
		if err := rows.Scan(
			&rec.TenantID, &rec.FingerprintHash, &rec.Frequency, &rec.FileCount,
			&rec.StabilityScore, &rec.AtomReuseRate,
			&rec.ProposedName, &dismissedAt,
			&firstSeen, &lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan promotion_candidate: %w", err)
		}
		if dismissedAt != "" {
			if t, e := time.Parse(time.RFC3339, dismissedAt); e == nil {
				rec.DismissedAt = t
			}
		}
		if t, e := time.Parse(time.RFC3339, firstSeen); e == nil {
			rec.FirstSeen = t
		}
		if t, e := time.Parse(time.RFC3339, lastSeen); e == nil {
			rec.LastSeen = t
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
