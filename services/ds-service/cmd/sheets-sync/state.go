package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// state.go — sheet_sync_state CRUD + per-cycle diff.
//
// Plan: see U7. Hash is SHA256 over the row's content fields (everything
// the inspector renders); LOAD reads all state; DIFF partitions current
// pull into new/changed/unchanged/gone; PERSIST writes back in one
// transaction.

// StateRow mirrors a row in sheet_sync_state.
type StateRow struct {
	SpreadsheetID  string
	Tab            string
	RowIndex       int
	TenantID       string
	FileID         string
	NodeID         string
	RowHash        string
	ProjectID      string
	FlowID         string
	LastSeenAt     string
	LastImportedAt string
	LastError      string
}

// stateKey is what dedups + diff buckets on.
type stateKey struct {
	SpreadsheetID string
	Tab           string
	RowIndex      int
}

// LoadState reads every state row for the spreadsheet into a map keyed
// on (spreadsheet, tab, row_index).
func LoadState(ctx context.Context, db *sql.DB, spreadsheetID string) (map[stateKey]StateRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT spreadsheet_id, tab, row_index, tenant_id, file_id, node_id, row_hash,
		        COALESCE(project_id, ''), COALESCE(flow_id, ''),
		        last_seen_at, COALESCE(last_imported_at, ''), COALESCE(last_error, '')
		   FROM sheet_sync_state
		  WHERE spreadsheet_id = ?`,
		spreadsheetID)
	if err != nil {
		return nil, fmt.Errorf("load_state: query: %w", err)
	}
	defer rows.Close()
	out := map[stateKey]StateRow{}
	for rows.Next() {
		var r StateRow
		if err := rows.Scan(&r.SpreadsheetID, &r.Tab, &r.RowIndex, &r.TenantID,
			&r.FileID, &r.NodeID, &r.RowHash,
			&r.ProjectID, &r.FlowID,
			&r.LastSeenAt, &r.LastImportedAt, &r.LastError); err != nil {
			return nil, fmt.Errorf("load_state: scan: %w", err)
		}
		out[stateKey{r.SpreadsheetID, r.Tab, r.RowIndex}] = r
	}
	return out, rows.Err()
}

// DiffBuckets is the result of comparing the current pull to existing state.
type DiffBuckets struct {
	New       []NormalizedRow
	Changed   []NormalizedRow
	Unchanged []NormalizedRow
	Gone      []StateRow
}

// Diff partitions the current pull (post-dedupe) against existing state.
//
// new       = key not in state
// changed   = key in state but row_hash differs
// unchanged = key in state, row_hash matches → skip work
// gone      = key in state, key not in current pull → soft-delete
func Diff(spreadsheetID string, current []NormalizedRow, state map[stateKey]StateRow) DiffBuckets {
	out := DiffBuckets{}
	currentKeys := make(map[stateKey]bool, len(current))

	for _, row := range current {
		k := stateKey{spreadsheetID, row.Tab, row.RowIndex}
		currentKeys[k] = true

		existing, ok := state[k]
		if !ok {
			out.New = append(out.New, row)
			continue
		}
		if existing.RowHash != row.RowHash {
			out.Changed = append(out.Changed, row)
		} else {
			out.Unchanged = append(out.Unchanged, row)
		}
	}

	for k, st := range state {
		if !currentKeys[k] {
			out.Gone = append(out.Gone, st)
		}
	}
	return out
}

// ComputeRowHash returns sha256 over the row's content-bearing fields.
// Fields included match what the inspector renders — so a typo fix in
// any of them flips the hash and triggers a re-import.
func ComputeRowHash(r NormalizedRow) string {
	parts := []string{
		r.FileID, r.NodeID,
		strings.TrimSpace(r.Project),
		strings.TrimSpace(r.ProductPOC),
		strings.TrimSpace(r.DesignerPOC),
		strings.TrimSpace(r.DRDURL),
		strings.TrimSpace(r.ProtoURL),
		strings.TrimSpace(r.StatusRaw),
		strings.TrimSpace(r.LastUpdated),
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])
}

// PersistRow upserts one StateRow. Caller wraps multiple PersistRow calls
// in a single transaction (see runCycle in main.go after U9 wires it).
func PersistRow(ctx context.Context, db *sql.DB, r StateRow) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO sheet_sync_state
		   (spreadsheet_id, tab, row_index, tenant_id, file_id, node_id, row_hash,
		    project_id, flow_id, last_seen_at, last_imported_at, last_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(spreadsheet_id, tab, row_index) DO UPDATE SET
		    tenant_id = excluded.tenant_id,
		    file_id = excluded.file_id,
		    node_id = excluded.node_id,
		    row_hash = excluded.row_hash,
		    project_id = excluded.project_id,
		    flow_id = excluded.flow_id,
		    last_seen_at = excluded.last_seen_at,
		    last_imported_at = COALESCE(excluded.last_imported_at, sheet_sync_state.last_imported_at),
		    last_error = excluded.last_error`,
		r.SpreadsheetID, r.Tab, r.RowIndex, r.TenantID, r.FileID, r.NodeID, r.RowHash,
		nilIfEmpty(r.ProjectID), nilIfEmpty(r.FlowID),
		r.LastSeenAt, nilIfEmpty(r.LastImportedAt), r.LastError)
	return err
}

// SoftDeleteFlow marks a flow row as deleted_at = now. Used for GONE
// rows. Idempotent — re-running on an already-deleted flow is a no-op.
func SoftDeleteFlow(ctx context.Context, db *sql.DB, flowID string, now time.Time) error {
	if flowID == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`UPDATE flows SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now.UTC().Format(time.RFC3339), flowID)
	return err
}

// RecordRun writes a sheet_sync_runs row at cycle end. Caller fills in
// the summary counts.
func RecordRun(ctx context.Context, db *sql.DB, run RunRecord) error {
	summaryJSON, _ := json.Marshal(run.Summary)
	_, err := db.ExecContext(ctx,
		`INSERT INTO sheet_sync_runs (id, spreadsheet_id, started_at, finished_at,
		    drive_modified_time, sheet_modified_time, result, summary_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.SpreadsheetID, run.StartedAt, run.FinishedAt,
		nilIfEmpty(run.DriveModifiedTime), nilIfEmpty(run.SheetModifiedTime),
		run.Result, string(summaryJSON))
	return err
}

// RunRecord is the audit row for one cycle.
type RunRecord struct {
	ID                string
	SpreadsheetID     string
	StartedAt         string // RFC3339
	FinishedAt        string // RFC3339, "" if mid-cycle
	DriveModifiedTime string
	SheetModifiedTime string
	Result            string         // unchanged | applied | failed | partial
	Summary           map[string]int // {new, changed, unchanged, gone, errors}
}

// LastSuccessfulRun reads the most recent run row that completed
// successfully — used by the modifiedTime gate to know what timestamp
// we already imported up to.
func LastSuccessfulRun(ctx context.Context, db *sql.DB, spreadsheetID string) (*RunRecord, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, spreadsheet_id, started_at, COALESCE(finished_at, ''),
		        COALESCE(drive_modified_time, ''), COALESCE(sheet_modified_time, ''),
		        result, summary_json
		   FROM sheet_sync_runs
		  WHERE spreadsheet_id = ? AND result IN ('applied', 'unchanged')
		  ORDER BY started_at DESC LIMIT 1`,
		spreadsheetID)
	var r RunRecord
	var summaryJSON string
	if err := row.Scan(&r.ID, &r.SpreadsheetID, &r.StartedAt, &r.FinishedAt,
		&r.DriveModifiedTime, &r.SheetModifiedTime,
		&r.Result, &summaryJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(summaryJSON), &r.Summary)
	return &r, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
