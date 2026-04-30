// Package projects — Phase 2 U9 — sidecar backfill helpers.
//
// One-shot ingestion of `lib/audit/*.json` per-file audit sidecars into the
// new SQLite tables (projects / project_versions / screens / violations).
// Used by `cmd/migrate-sidecars`.
//
// Repository observation (2026-04-30): the project today has no per-file
// audit sidecars under `lib/audit/`. The directory contains only
// `index.json` (audit-files manifest) and `spacing-observed.json`
// (cross-file aggregate). The brainstorm referenced "~800 sidecars" — this
// is aspirational; designers haven't run per-file audits yet.
//
// The CLI still ships in working form so:
//   - When designers DO run per-file audits in Phase 2 deployment, the
//     migration tool already exists.
//   - Tests exercise the parsing + idempotency paths against synthetic
//     fixtures (no dependence on real sidecar data).
//
// Synthetic project shape (one per sidecar slug):
//
//	platform = "web"
//	product  = "DesignSystem"
//	path     = "docs/<slug>"
//	tenant_id = system tenant (configurable via DS_SYSTEM_TENANT_ID)
//	owner_user_id = system user  (configurable via DS_SYSTEM_USER_ID)

package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
)

// SystemTenantID is read from env DS_SYSTEM_TENANT_ID (default "system").
// SystemUserID is read from env DS_SYSTEM_USER_ID (default "system").
//
// The CLI calls SetSystemIdentity once at boot before any backfill calls.

// Backfiller ingests audit sidecars into SQLite. One Backfiller per CLI run.
//
// Inputs are sidecar bytes + slug + mtime; the Backfiller doesn't touch the
// filesystem itself so unit tests can drive it with fixtures.
type Backfiller struct {
	DB             *sql.DB
	SystemTenantID string
	SystemUserID   string
	Now            func() time.Time
}

// BackfillResult captures what BackfillSidecar did for one sidecar.
type BackfillResult struct {
	Slug          string
	Action        string // "skip_unchanged" | "created" | "updated"
	ProjectID     string
	VersionID     string
	ScreenCount   int
	ViolationCount int
}

// BackfillSidecar persists one audit sidecar into SQLite. Idempotent on
// (slug, mtime): re-running on an unchanged sidecar returns Action=skip_unchanged
// without writing anything.
//
// The sidecar bytes must parse as audit.AuditResult.
func (b *Backfiller) BackfillSidecar(ctx context.Context, sourcePath, slug string, sidecarBytes []byte, mtime time.Time) (*BackfillResult, error) {
	if b.DB == nil {
		return nil, errors.New("backfill: nil DB")
	}
	if b.SystemTenantID == "" {
		return nil, errors.New("backfill: SystemTenantID required")
	}
	if b.SystemUserID == "" {
		return nil, errors.New("backfill: SystemUserID required")
	}
	now := b.now()

	var result audit.AuditResult
	if err := json.Unmarshal(sidecarBytes, &result); err != nil {
		return nil, fmt.Errorf("parse sidecar %q: %w", slug, err)
	}

	// Check existing project + marker.
	projectID, prevMtime, err := b.lookupProject(ctx, slug)
	if err != nil {
		return nil, err
	}
	if projectID != "" && prevMtime != 0 && prevMtime >= mtime.Unix() {
		return &BackfillResult{
			Slug:      slug,
			Action:    "skip_unchanged",
			ProjectID: projectID,
		}, nil
	}

	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	action := "created"
	if projectID != "" {
		action = "updated"
	} else {
		projectID = uuid.NewString()
		if err := b.insertSyntheticProject(ctx, tx, projectID, slug, now); err != nil {
			return nil, err
		}
	}

	versionID, versionIndex, err := b.nextVersion(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if err := b.insertVersion(ctx, tx, versionID, projectID, versionIndex, now); err != nil {
		return nil, err
	}

	flowID, err := b.ensureSyntheticFlow(ctx, tx, projectID, slug, now)
	if err != nil {
		return nil, err
	}

	screenIDs, screenCount, err := b.insertScreens(ctx, tx, versionID, flowID, result.Screens, now)
	if err != nil {
		return nil, err
	}

	violationCount, err := b.insertViolations(ctx, tx, versionID, screenIDs, result.Screens, now)
	if err != nil {
		return nil, err
	}

	if err := b.upsertMarker(ctx, tx, projectID, sourcePath, mtime, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &BackfillResult{
		Slug:           slug,
		Action:         action,
		ProjectID:      projectID,
		VersionID:      versionID,
		ScreenCount:    screenCount,
		ViolationCount: violationCount,
	}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func (b *Backfiller) now() time.Time {
	if b.Now == nil {
		return time.Now().UTC()
	}
	return b.Now().UTC()
}

func (b *Backfiller) lookupProject(ctx context.Context, slug string) (string, int64, error) {
	var projectID string
	var mtime sql.NullInt64
	err := b.DB.QueryRowContext(ctx,
		`SELECT p.id, COALESCE(m.sidecar_mtime, 0)
		   FROM projects p
		   LEFT JOIN backfill_markers m ON m.project_id = p.id
		  WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL`,
		slug, b.SystemTenantID,
	).Scan(&projectID, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if mtime.Valid {
		return projectID, mtime.Int64, nil
	}
	return projectID, 0, nil
}

func (b *Backfiller) insertSyntheticProject(ctx context.Context, tx *sql.Tx, projectID, slug string, now time.Time) error {
	humanName := humanize(slug)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		 VALUES (?, ?, ?, 'web', 'DesignSystem', ?, ?, ?, ?, ?)`,
		projectID, slug, humanName, "docs/"+slug,
		b.SystemUserID, b.SystemTenantID,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return err
}

func (b *Backfiller) nextVersion(ctx context.Context, tx *sql.Tx, projectID string) (string, int, error) {
	var maxIdx sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(version_index) FROM project_versions WHERE project_id = ?`,
		projectID,
	).Scan(&maxIdx); err != nil {
		return "", 0, err
	}
	next := 1
	if maxIdx.Valid {
		next = int(maxIdx.Int64) + 1
	}
	return uuid.NewString(), next, nil
}

func (b *Backfiller) insertVersion(ctx context.Context, tx *sql.Tx, versionID, projectID string, versionIndex int, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO project_versions
		   (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, 'view_ready', ?, ?)`,
		versionID, projectID, b.SystemTenantID, versionIndex,
		b.SystemUserID, now.Format(time.RFC3339),
	)
	return err
}

func (b *Backfiller) ensureSyntheticFlow(ctx context.Context, tx *sql.Tx, projectID, slug string, now time.Time) (string, error) {
	var flowID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM flows WHERE project_id = ? AND tenant_id = ? AND deleted_at IS NULL LIMIT 1`,
		projectID, b.SystemTenantID,
	).Scan(&flowID)
	if err == nil {
		return flowID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	flowID = uuid.NewString()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		flowID, projectID, b.SystemTenantID,
		"sidecar:"+slug, "Components",
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return flowID, err
}

func (b *Backfiller) insertScreens(ctx context.Context, tx *sql.Tx, versionID, flowID string, screens []audit.AuditScreen, now time.Time) (map[string]string, int, error) {
	ids := make(map[string]string, len(screens)) // node_id → screen_id
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO screens
		   (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, 0, err
	}
	defer stmt.Close()
	for i, s := range screens {
		id := uuid.NewString()
		if _, err := stmt.ExecContext(ctx,
			id, versionID, flowID, b.SystemTenantID,
			0.0, float64(i*1000), 1024.0, 800.0,
			s.NodeID, now.Format(time.RFC3339),
		); err != nil {
			return nil, 0, err
		}
		ids[s.NodeID] = id
	}
	return ids, len(screens), nil
}

func (b *Backfiller) insertViolations(ctx context.Context, tx *sql.Tx, versionID string, screenIDs map[string]string, screens []audit.AuditScreen, now time.Time) (int, error) {
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO violations
		   (id, version_id, screen_id, tenant_id, rule_id, severity, category,
		    property, observed, suggestion, status, auto_fixable, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	count := 0
	for _, screen := range screens {
		screenID := screenIDs[screen.NodeID]
		for _, fc := range screen.Fixes {
			ruleID := ruleIDForBackfill(fc)
			severity := MapPriorityToSeverity(fc)
			category := backfillCategoryFor(ruleID)
			autoFix := 0
			suggestion := suggestionForBackfill(fc)
			if suggestion != "" {
				switch fc.Reason {
				case "drift", "deprecated", "unbound":
					autoFix = 1
				}
			}
			if _, err := stmt.ExecContext(ctx,
				uuid.NewString(), versionID, screenID, b.SystemTenantID,
				ruleID, severity, category,
				fc.Property, fc.Observed, suggestion,
				autoFix, now.Format(time.RFC3339),
			); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

func (b *Backfiller) upsertMarker(ctx context.Context, tx *sql.Tx, projectID, sourcePath string, mtime time.Time, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO backfill_markers (project_id, source_path, sidecar_mtime, last_run_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET
		   source_path = excluded.source_path,
		   sidecar_mtime = excluded.sidecar_mtime,
		   last_run_at = excluded.last_run_at`,
		projectID, sourcePath, mtime.Unix(), now.Format(time.RFC3339),
	)
	return err
}

// ─── Pure helpers ───────────────────────────────────────────────────────────

func ruleIDForBackfill(fc audit.FixCandidate) string {
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

func suggestionForBackfill(fc audit.FixCandidate) string {
	if fc.TokenPath != "" {
		return "Bind to " + fc.TokenPath
	}
	if fc.ReplacedBy != "" {
		return "Replace deprecated token with " + fc.ReplacedBy
	}
	return fc.Rationale
}

// backfillCategoryFor mirrors the migration 0002 backfill UPDATE mapping so
// the CLI produces violations.category values consistent with what the live
// runner emits going forward.
func backfillCategoryFor(ruleID string) string {
	switch {
	case strings.HasPrefix(ruleID, "theme_break."):
		return "theme_parity"
	case ruleID == "drift.text" || ruleID == "deprecated.text" || ruleID == "unbound.text":
		return "text_style_drift"
	case ruleID == "drift.padding" || ruleID == "drift.gap" || ruleID == "drift.spacing":
		return "spacing_drift"
	case ruleID == "drift.radius":
		return "radius_drift"
	case ruleID == "unbound.component" || ruleID == "drift.component":
		return "component_match"
	case ruleID == "custom.component":
		return "component_governance"
	default:
		// fills, strokes, deprecated.* fall here
		return "token_drift"
	}
}

// humanize turns "my-flow-name" into "My Flow Name".
func humanize(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
