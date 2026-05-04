package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"
)

// sheets.go — Sheets API v4 reader.
//
// Reads the entire sheet in two API calls per cycle:
//   1. spreadsheets.get — list every tab + properties
//   2. spreadsheets.values.batchGet — fetch values for every visible tab
//
// Output is a flat in-memory representation the rest of the pipeline
// (normalize → dedupe → diff → export) consumes. We deliberately don't
// stream — total sheet payload is ~50 KB even with 24 tabs and 1000+
// rows, so a single batch read is the simplest correct option.

// Spreadsheet is the in-memory shape the pipeline reads.
type Spreadsheet struct {
	ID       string
	Title    string
	TimeZone string
	Tabs     []Tab
}

// Tab is one sheet within the spreadsheet.
type Tab struct {
	Name   string
	GID    int64
	Hidden bool
	Header []string
	Rows   [][]string
}

// Caps to keep the read bounded — same as the Apps Script we're replacing.
const (
	maxRowsPerTab = 5000
	maxColsPerTab = 26 // A..Z
)

// FetchAll pulls the spreadsheet's tab list + values for every visible
// tab in two API calls.
//
// Filtering: tabs in the skip set (per parse.go SubSheetToProduct returning
// Skip=true) are still included in the returned struct so callers can log
// them — the orchestrator skips them at the dedup step. Empty rows
// (entirely-blank) are filtered here since they're never useful and inflate
// the row_index space.
func FetchAll(ctx context.Context, credsPath, spreadsheetID string) (*Spreadsheet, error) {
	svc, err := sheetsapi.NewService(ctx,
		option.WithCredentialsFile(credsPath),
		option.WithScopes(sheetsapi.SpreadsheetsReadonlyScope),
	)
	if err != nil {
		return nil, fmt.Errorf("sheets: new service: %w", err)
	}

	// Step 1 — list tabs.
	meta, err := svc.Spreadsheets.Get(spreadsheetID).
		IncludeGridData(false).
		Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets: get spreadsheet: %w", err)
	}

	out := &Spreadsheet{
		ID:    spreadsheetID,
		Title: meta.Properties.Title,
	}
	if meta.Properties != nil {
		out.TimeZone = meta.Properties.TimeZone
	}

	// Build the list of ranges we'll batchGet — one per visible tab.
	// Quote tab names that contain special chars (the existing sheet has
	// "BBPS &TPAP" with a space and ampersand).
	ranges := make([]string, 0, len(meta.Sheets))
	tabMeta := make([]Tab, 0, len(meta.Sheets))
	for _, s := range meta.Sheets {
		if s.Properties == nil {
			continue
		}
		hidden := s.Properties.Hidden
		if hidden {
			// Mirror the existing inspector behavior — skip hidden tabs.
			continue
		}
		name := s.Properties.Title
		ranges = append(ranges, fmt.Sprintf("'%s'!A1:Z%d", strings.ReplaceAll(name, "'", "''"), maxRowsPerTab))
		tabMeta = append(tabMeta, Tab{
			Name:   name,
			GID:    s.Properties.SheetId,
			Hidden: hidden,
		})
	}
	if len(ranges) == 0 {
		return out, nil
	}

	// Step 2 — batch read values.
	resp, err := svc.Spreadsheets.Values.BatchGet(spreadsheetID).
		Ranges(ranges...).
		MajorDimension("ROWS").
		ValueRenderOption("FORMATTED_VALUE").
		Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets: batchGet values: %w", err)
	}
	if len(resp.ValueRanges) != len(tabMeta) {
		return nil, fmt.Errorf("sheets: batchGet returned %d ranges, expected %d", len(resp.ValueRanges), len(tabMeta))
	}

	for i, vr := range resp.ValueRanges {
		t := tabMeta[i]
		values := vr.Values
		if len(values) == 0 {
			out.Tabs = append(out.Tabs, t)
			continue
		}
		// Header row + data rows
		header := stringifyRow(values[0])
		t.Header = header
		t.Rows = make([][]string, 0, len(values)-1)
		for _, raw := range values[1:] {
			row := stringifyRow(raw)
			if isBlankRow(row) {
				continue
			}
			// Cap column count so a sheet with stray columns doesn't blow up
			if len(row) > maxColsPerTab {
				row = row[:maxColsPerTab]
			}
			t.Rows = append(t.Rows, row)
		}
		out.Tabs = append(out.Tabs, t)
	}
	return out, nil
}

// stringifyRow flattens a Sheets API value row (each cell is interface{}
// because cells can be string / number / bool) to strings. Empty cells
// become empty strings.
func stringifyRow(in []any) []string {
	out := make([]string, len(in))
	for i, v := range in {
		switch t := v.(type) {
		case string:
			out[i] = t
		case nil:
			out[i] = ""
		default:
			out[i] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

// isBlankRow returns true when every cell is whitespace-only.
func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// HeaderColumn looks up the index of a named column in the tab's header
// row. Case-insensitive, whitespace-tolerant. Returns -1 when not found.
//
// The pipeline reads by COLUMN NAME (Project, Product POC, Design POC,
// DRD link, Figma link, Proto link, Status, Last updated status) rather
// than fixed letter, so a column reorder by the team doesn't break sync.
func HeaderColumn(t *Tab, name string) int {
	target := normalizeHeader(name)
	for i, h := range t.Header {
		if normalizeHeader(h) == target {
			return i
		}
	}
	return -1
}

func normalizeHeader(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	// Collapse whitespace to single spaces
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}
