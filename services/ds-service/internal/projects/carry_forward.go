package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Phase 4 U3 — Dismissed-violation carry-forward.
//
// Two halves work together:
//
//  1. ApplyCarryForwardInTx is called by the worker inside
//     PersistRunIdempotent's transaction, BEFORE violations are inserted.
//     It mutates the slice in place — any new violation whose
//     (screen_logical_id, rule_id, property) matches a previously-dismissed
//     marker has its Status flipped to "dismissed". The reason itself stays
//     on the dismissed_carry_forwards row; serializers JOIN at read time.
//
//  2. WriteCarryForwardMarker / DeleteCarryForwardMarker are called by the
//     lifecycle endpoint (server.go HandlePatchViolation) inside its tx
//     when a designer dismisses a violation (insert) or an admin reactivates
//     a dismissed one (delete). Calling lifecycle.go's transition validator
//     first ensures these only run for legal flips.

// CarryForwardMarker is the persisted shape (one row in
// dismissed_carry_forwards). The PK is (tenant_id, screen_logical_id,
// rule_id, property).
type CarryForwardMarker struct {
	TenantID            string
	ScreenLogicalID     string
	RuleID              string
	Property            string
	Reason              string
	DismissedByUserID   string
	DismissedAt         time.Time
	OriginalViolationID string
}

// carryForwardKey is the in-memory triple used to hit-test new violations
// against the markers table. Lifted to package level so the helpers all
// agree on the type.
type carryForwardKey struct {
	Logical  string
	Rule     string
	Property string
}

// ApplyCarryForwardInTx scans the violation slice and flips Status to
// "dismissed" for any row matching an existing carry-forward marker. Runs
// inside the worker's PersistRunIdempotent transaction so the read of
// dismissed_carry_forwards and the violation INSERT see a consistent
// snapshot.
//
// Lookup is done by joining the candidate violations against the markers
// table via a single IN-clause keyed by (screen_logical_id, rule_id,
// property). The screen_logical_id is fetched up-front for the candidate
// screens; we don't have it on the Violation struct because the worker
// receives violations indexed by screen_id (the per-version row id), not
// the stable logical id.
//
// Returns the count of flipped rows so callers can log/metric the carry-
// forward fan-out.
func ApplyCarryForwardInTx(ctx context.Context, tx *sql.Tx, tenantID string, violations []Violation) (int, error) {
	if len(violations) == 0 {
		return 0, nil
	}

	// Build the unique screen_id set so we resolve screen_logical_id with one
	// query instead of one per violation.
	screenIDSet := make(map[string]struct{})
	for _, v := range violations {
		if v.ScreenID != "" {
			screenIDSet[v.ScreenID] = struct{}{}
		}
	}
	if len(screenIDSet) == 0 {
		return 0, nil
	}

	logicalByScreenID, err := loadLogicalIDsForScreens(ctx, tx, tenantID, screenIDSet)
	if err != nil {
		return 0, fmt.Errorf("load logical ids: %w", err)
	}
	if len(logicalByScreenID) == 0 {
		return 0, nil
	}

	// Build the marker lookup set keyed by (logical_id, rule_id, property).
	candidates := make([]*Violation, 0, len(violations))
	keys := make([]carryForwardKey, 0, len(violations))
	seen := make(map[carryForwardKey]struct{}, len(violations))
	for i := range violations {
		v := &violations[i]
		logical, ok := logicalByScreenID[v.ScreenID]
		if !ok || logical == "" {
			continue
		}
		k := carryForwardKey{Logical: logical, Rule: v.RuleID, Property: v.Property}
		if _, dup := seen[k]; !dup {
			keys = append(keys, k)
			seen[k] = struct{}{}
		}
		candidates = append(candidates, v)
	}
	if len(keys) == 0 {
		return 0, nil
	}

	// Single query: SELECT ... WHERE (logical, rule, property) IN ((...), ...)
	// SQLite supports the row-value IN form, but for portability and easier
	// parameterization we expand into per-row OR clauses. With 50-frame
	// flows × ~10 rules each, this is at most ~500 OR predicates — well
	// within SQLite's parser limits.
	matched, err := loadCarryForwardMarkers(ctx, tx, tenantID, keys)
	if err != nil {
		return 0, fmt.Errorf("load markers: %w", err)
	}
	if len(matched) == 0 {
		return 0, nil
	}

	flipped := 0
	for _, v := range candidates {
		logical := logicalByScreenID[v.ScreenID]
		k := carryForwardKey{Logical: logical, Rule: v.RuleID, Property: v.Property}
		if _, hit := matched[k]; !hit {
			continue
		}
		v.Status = ViolationStatusDismissed
		flipped++
	}
	return flipped, nil
}

// loadLogicalIDsForScreens resolves a batch of screen_id → screen_logical_id
// scoped to the tenant. Cross-tenant rows are silently dropped from the
// returned map (their match would be unsafe to act on).
func loadLogicalIDsForScreens(ctx context.Context, tx *sql.Tx, tenantID string, screenIDs map[string]struct{}) (map[string]string, error) {
	if len(screenIDs) == 0 {
		return map[string]string{}, nil
	}
	args := make([]any, 0, len(screenIDs)+1)
	for id := range screenIDs {
		args = append(args, id)
	}
	args = append(args, tenantID)
	q := `SELECT id, screen_logical_id FROM screens
	      WHERE id IN (` + strings.Repeat("?,", len(screenIDs)-1) + `?) AND tenant_id = ?`

	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(screenIDs))
	for rows.Next() {
		var id, logical string
		if err := rows.Scan(&id, &logical); err != nil {
			return nil, err
		}
		out[id] = logical
	}
	return out, rows.Err()
}

// loadCarryForwardMarkers returns the set of (logical, rule, property)
// triples that have a marker row for this tenant. A map is returned
// (rather than a slice) so the caller can do O(1) hit-test on each
// candidate violation.
func loadCarryForwardMarkers(ctx context.Context, tx *sql.Tx, tenantID string, keys []carryForwardKey) (map[carryForwardKey]struct{}, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	// Build (logical = ? AND rule = ? AND property = ?) OR (...) OR ...
	clauses := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*3+1)
	for _, k := range keys {
		clauses = append(clauses, "(screen_logical_id = ? AND rule_id = ? AND property = ?)")
		args = append(args, k.Logical, k.Rule, k.Property)
	}
	args = append(args, tenantID)
	q := `SELECT screen_logical_id, rule_id, property FROM dismissed_carry_forwards
	      WHERE (` + strings.Join(clauses, " OR ") + `) AND tenant_id = ?`

	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[carryForwardKey]struct{})
	for rows.Next() {
		var logical, rule, prop string
		if err := rows.Scan(&logical, &rule, &prop); err != nil {
			return nil, err
		}
		out[carryForwardKey{Logical: logical, Rule: rule, Property: prop}] = struct{}{}
	}
	return out, rows.Err()
}

// WriteCarryForwardMarker upserts a marker row when a violation transitions
// to Dismissed. Uses INSERT OR REPLACE so re-dismissing the same identity
// updates the reason without raising a unique-constraint error. Runs inside
// the lifecycle endpoint's transaction.
//
// screenLogicalID is resolved by the caller from violations.screen_id. We
// don't fetch it here so the lifecycle path remains a single-statement tx
// in the common case.
func WriteCarryForwardMarker(ctx context.Context, tx *sql.Tx, m CarryForwardMarker) error {
	_, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO dismissed_carry_forwards
		 (tenant_id, screen_logical_id, rule_id, property,
		  reason, dismissed_by_user_id, dismissed_at, original_violation_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TenantID, m.ScreenLogicalID, m.RuleID, m.Property,
		m.Reason, m.DismissedByUserID,
		m.DismissedAt.UTC().Format(time.RFC3339Nano),
		m.OriginalViolationID,
	)
	return err
}

// DeleteCarryForwardMarker removes the marker for a (tenant, logical, rule,
// property) tuple. Called when a Dismissed violation is reactivated by an
// admin so the next re-audit re-emits it as Active.
//
// Returns nil even when no row exists — the override path is idempotent
// from the caller's perspective.
func DeleteCarryForwardMarker(ctx context.Context, tx *sql.Tx, tenantID, screenLogicalID, ruleID, property string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM dismissed_carry_forwards
		 WHERE tenant_id = ? AND screen_logical_id = ?
		   AND rule_id = ? AND property = ?`,
		tenantID, screenLogicalID, ruleID, property,
	)
	return err
}

// ResolveCarryForwardKey looks up the (screen_logical_id, rule_id, property)
// for a given violation. Used by the lifecycle endpoint to know what marker
// to write/delete without round-tripping the carry-forward fields through
// the request body. The query joins violations → screens to read the
// screen_logical_id; tenant-scoped to make cross-tenant return ErrNotFound
// (no oracle).
func ResolveCarryForwardKey(ctx context.Context, db *sql.DB, tenantID, violationID string) (logicalID, ruleID, property string, err error) {
	row := db.QueryRowContext(ctx,
		`SELECT s.screen_logical_id, v.rule_id, v.property
		   FROM violations v
		   JOIN screens s ON s.id = v.screen_id
		  WHERE v.id = ? AND v.tenant_id = ?`,
		violationID, tenantID,
	)
	if scanErr := row.Scan(&logicalID, &ruleID, &property); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return "", "", "", ErrNotFound
		}
		return "", "", "", scanErr
	}
	return logicalID, ruleID, property, nil
}
