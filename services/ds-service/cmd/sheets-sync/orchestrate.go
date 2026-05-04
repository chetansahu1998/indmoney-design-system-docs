package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // sqlite driver for state DB
	"github.com/google/uuid"
)

// orchestrate.go — top-level cycle, wires every step together.
//
// Replaces the stub runCycle in main.go. One function call from main()
// produces:
//   1. Drive modifiedTime probe (skip cycle when unchanged)
//   2. Sheets batchGet
//   3. Per-row normalize (parse, classify, hash, resolve)
//   4. Cross-tab dedup
//   5. State diff (new / changed / unchanged / gone)
//   6. For new+changed: Figma resolve → POST export → persist state
//   7. For gone: soft-delete the flow + telemetry
//   8. Per-cycle telemetry summary + sheet_sync_runs row

// runCycleImpl is the production cycle body. main.go calls runCycle
// (which delegates here so the stub can stay for tests).
func runCycleImpl(ctx context.Context, cfg *config, dbPath string) error {
	log := cfg.Logger
	startedAt := time.Now().UTC()
	runID := uuid.NewString()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	// ── Step 1: Drive modifiedTime gate
	driveMtime, driveErr := ProbeModifiedTime(ctx, cfg.SACredsPath, cfg.SpreadsheetID)
	if driveErr != nil {
		log.Warn("drive_probe_failed_continuing", "err", driveErr.Error())
		// Graceful degrade — proceed with the full pull.
	}
	prev, _ := LastSuccessfulRun(ctx, db, cfg.SpreadsheetID)
	if driveErr == nil && prev != nil && prev.DriveModifiedTime != "" {
		if prevTime, perr := time.Parse(time.RFC3339, prev.DriveModifiedTime); perr == nil && !driveMtime.After(prevTime) {
			log.Info("cycle.unchanged",
				"spreadsheet_id", cfg.SpreadsheetID,
				"drive_modified_time", driveMtime.Format(time.RFC3339),
				"prev_run_at", prev.StartedAt,
			)
			emitTelemetry(cfg, "info", "sheets.sync.unchanged", map[string]any{
				"drive_modified_time": driveMtime.Format(time.RFC3339),
			})
			// Still record the cycle so the audit log is complete.
			return RecordRun(ctx, db, RunRecord{
				ID:                runID,
				SpreadsheetID:     cfg.SpreadsheetID,
				StartedAt:         startedAt.Format(time.RFC3339),
				FinishedAt:        time.Now().UTC().Format(time.RFC3339),
				DriveModifiedTime: driveMtime.Format(time.RFC3339),
				Result:            "unchanged",
				Summary:           map[string]int{},
			})
		}
	}

	// ── Step 2: Sheets batchGet
	sheet, err := FetchAll(ctx, cfg.SACredsPath, cfg.SpreadsheetID)
	if err != nil {
		emitTelemetry(cfg, "error", "sheets.sync.fetch_failed", map[string]any{"err": err.Error()})
		_ = recordFailed(ctx, db, runID, cfg.SpreadsheetID, startedAt, driveMtime, err.Error())
		return err
	}
	log.Info("sheet.fetched",
		"title", sheet.Title,
		"tabs", len(sheet.Tabs),
	)

	// ── Step 3: per-row normalize
	tenantID := os.Getenv("DS_TENANT_ID")
	if tenantID == "" {
		tenantID = "e090530f-2698-489d-934a-c821cb925c8a" // dev default; Fly secret overrides
	}
	rows := normalizeAllRows(sheet, cfg.TabOnly, cfg.RowLimit)
	log.Info("rows.normalized", "count", len(rows))

	// ── Step 4: cross-tab dedup
	rows, dedupWarns := Dedupe(rows)
	for _, w := range dedupWarns {
		log.Info("dedup.warning", "msg", w)
		emitTelemetry(cfg, "warn", "sheets.sync.dedup_warning", map[string]any{"msg": w})
	}

	// ── Step 5: state diff
	state, err := LoadState(ctx, db, cfg.SpreadsheetID)
	if err != nil {
		_ = recordFailed(ctx, db, runID, cfg.SpreadsheetID, startedAt, driveMtime, err.Error())
		return err
	}
	buckets := Diff(cfg.SpreadsheetID, rows, state)
	log.Info("diff.partitions",
		"new", len(buckets.New),
		"changed", len(buckets.Changed),
		"unchanged", len(buckets.Unchanged),
		"gone", len(buckets.Gone),
	)

	// ── Step 6: process NEW + CHANGED (skip rows without a valid Figma URL)
	figma := NewFigmaClient(cfg.FigmaPAT)
	gdoc := NewGDocFetcher(cfg.SACredsPath)
	exporter := NewExporter(cfg.DSServiceURL, cfg.DSServiceJWT, gdoc, cfg.DryRun)

	summary := map[string]int{
		"new":       len(buckets.New),
		"changed":   len(buckets.Changed),
		"unchanged": len(buckets.Unchanged),
		"gone":      len(buckets.Gone),
		"errors":    0,
	}
	now := time.Now().UTC()

	for _, row := range append(append([]NormalizedRow{}, buckets.New...), buckets.Changed...) {
		st := StateRow{
			SpreadsheetID: cfg.SpreadsheetID,
			Tab:           row.Tab,
			RowIndex:      row.RowIndex,
			TenantID:      tenantID,
			FileID:        row.FileID,
			NodeID:        row.NodeID,
			RowHash:       row.RowHash,
			LastSeenAt:    now.Format(time.RFC3339),
		}

		// Skip Figma resolve for ghost / canvas-only / malformed rows —
		// they get a state entry but no flow.
		if row.URLKind != URLValid {
			st.LastError = ""
			if !cfg.DryRun {
				_ = PersistRow(ctx, db, st)
			}
			continue
		}

		screens, ferr := figma.ResolveSection(ctx, row.FileID, row.NodeID)
		if ferr != nil {
			summary["errors"]++
			st.LastError = ferr.Error()
			emitTelemetry(cfg, "error", "sheets.sync.figma_resolve_failed", map[string]any{
				"tab": row.Tab, "row_index": row.RowIndex,
				"file_id": row.FileID, "node_id": row.NodeID,
				"err": ferr.Error(),
			})
			if !cfg.DryRun {
				_ = PersistRow(ctx, db, st)
			}
			continue
		}

		resp, eerr := exporter.ExportRow(ctx, row, screens)
		if eerr != nil {
			summary["errors"]++
			st.LastError = eerr.Error()
			emitTelemetry(cfg, "error", "sheets.sync.export_failed", map[string]any{
				"tab": row.Tab, "row_index": row.RowIndex, "err": eerr.Error(),
			})
			if !cfg.DryRun {
				_ = PersistRow(ctx, db, st)
			}
			continue
		}

		st.ProjectID = resp.ProjectID
		st.LastImportedAt = now.Format(time.RFC3339)
		st.LastError = ""
		if !cfg.DryRun {
			_ = PersistRow(ctx, db, st)
		}
	}

	// ── Step 7: GONE rows — soft-delete the flow
	for _, st := range buckets.Gone {
		if st.FlowID != "" && !cfg.DryRun {
			if err := SoftDeleteFlow(ctx, db, st.FlowID, now); err != nil {
				log.Warn("soft_delete_failed", "flow_id", st.FlowID, "err", err.Error())
			}
		}
		emitTelemetry(cfg, "info", "sheets.sync.row_gone", map[string]any{
			"tab": st.Tab, "row_index": st.RowIndex,
			"flow_id": st.FlowID,
		})
	}

	// ── Step 8: cycle summary
	finishedAt := time.Now().UTC()
	result := "applied"
	if summary["errors"] > 0 && (summary["errors"] >= summary["new"]+summary["changed"]) {
		result = "failed"
	} else if summary["errors"] > 0 {
		result = "partial"
	}
	emitTelemetry(cfg, "info", "sheets.sync.cycle.done", map[string]any{
		"result":      result,
		"new":         summary["new"],
		"changed":     summary["changed"],
		"unchanged":   summary["unchanged"],
		"gone":        summary["gone"],
		"errors":      summary["errors"],
		"duration_ms": finishedAt.Sub(startedAt).Milliseconds(),
	})
	if !cfg.DryRun {
		_ = RecordRun(ctx, db, RunRecord{
			ID:                runID,
			SpreadsheetID:     cfg.SpreadsheetID,
			StartedAt:         startedAt.Format(time.RFC3339),
			FinishedAt:        finishedAt.Format(time.RFC3339),
			DriveModifiedTime: driveMtimeStr(driveMtime),
			Result:            result,
			Summary:           summary,
		})
	}
	log.Info("cycle.done", "result", result,
		"new", summary["new"], "changed", summary["changed"],
		"unchanged", summary["unchanged"], "gone", summary["gone"],
		"errors", summary["errors"],
		"duration_ms", finishedAt.Sub(startedAt).Milliseconds(),
	)
	return nil
}

// normalizeAllRows turns the raw Sheets payload into NormalizedRows.
// Skips tabs marked Skip=true by SubSheetToProduct, applies --tab and
// --limit flags, computes per-row hash + classifications.
func normalizeAllRows(sheet *Spreadsheet, tabOnly string, rowLimit int) []NormalizedRow {
	var out []NormalizedRow
	for _, tab := range sheet.Tabs {
		if tabOnly != "" && tab.Name != tabOnly {
			continue
		}
		mapping := SubSheetToProduct(tab.Name)
		if mapping.Skip {
			continue
		}
		colProj := HeaderColumn(&tab, "Project")
		colPM := HeaderColumn(&tab, "Product POC")
		colDes := HeaderColumn(&tab, "Design POC")
		colDRD := HeaderColumn(&tab, "DRD link")
		colFig := HeaderColumn(&tab, "Figma link")
		colProto := HeaderColumn(&tab, "Proto link")
		colStatus := HeaderColumn(&tab, "Status")
		colLast := HeaderColumn(&tab, "Last updated status")

		max := len(tab.Rows)
		if rowLimit > 0 && rowLimit < max {
			max = rowLimit
		}
		for i := 0; i < max; i++ {
			r := tab.Rows[i]
			row := NormalizedRow{
				Tab:         tab.Name,
				RowIndex:    i + 2, // header is 1, data starts at 2
				Project:     cellAt(r, colProj),
				ProductPOC:  cellAt(r, colPM),
				DesignerPOC: cellAt(r, colDes),
				DRDURL:      cellAt(r, colDRD),
				FigmaURL:    cellAt(r, colFig),
				ProtoURL:    cellAt(r, colProto),
				StatusRaw:   cellAt(r, colStatus),
				LastUpdated: cellAt(r, colLast),
				Mapping:     mapping,
			}
			row.URLKind, row.FileID, row.NodeID = ParseFigmaURL(row.FigmaURL)
			row.DRDKind, row.GDocID = ClassifyDRD(row.DRDURL)
			row.StatusNorm = NormalizeStatus(row.StatusRaw)
			row.RowHash = ComputeRowHash(row)
			out = append(out, row)
		}
	}
	return out
}

func cellAt(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[col])
}

func recordFailed(ctx context.Context, db *sql.DB, runID, spreadsheetID string, startedAt time.Time, driveMtime time.Time, errMsg string) error {
	return RecordRun(ctx, db, RunRecord{
		ID:                runID,
		SpreadsheetID:     spreadsheetID,
		StartedAt:         startedAt.Format(time.RFC3339),
		FinishedAt:        time.Now().UTC().Format(time.RFC3339),
		DriveModifiedTime: driveMtimeStr(driveMtime),
		Result:            "failed",
		Summary:           map[string]int{"err": 1},
	})
}

func driveMtimeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// emitTelemetry POSTs to ds-service /v1/telemetry/event. Best-effort —
// logs the failure but never blocks the cycle.
func emitTelemetry(cfg *config, level, event string, payload map[string]any) {
	body := map[string]any{
		"source":  "sheets-sync",
		"level":   level,
		"event":   event,
		"payload": payload,
		"build":   "2026-05-05",
	}
	jb, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, cfg.DSServiceURL+"/v1/telemetry/event", strings.NewReader(string(jb)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		cfg.Logger.Debug("telemetry_post_failed", "err", err.Error())
		return
	}
	resp.Body.Close()
}
