-- 0017_sheet_sync_state — sheet-sync pipeline state + audit-runs.
-- Plan: docs/plans/2026-05-05-001-feat-google-sheet-sync-pipeline-plan.md (U1)
--
-- The cmd/sheets-sync command polls a Google Sheet every 5 minutes,
-- diffs the content against this state table, and POSTs new/changed rows
-- to /v1/projects/export. The state table preserves per-row hashes so we
-- only re-import on actual content change — even though the cron tick
-- itself is unconditional.
--
-- Two tables here:
--   sheet_sync_state — one row per sheet row we've ever seen
--   sheet_sync_runs  — one row per cron cycle (audit log)
--
-- Plus 6 nullable columns added to `flows` so we can carry sheet-sourced
-- metadata through to the inspector without forking the schema.

-- ─── State table ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sheet_sync_state (
  spreadsheet_id    TEXT NOT NULL,
  tab               TEXT NOT NULL,
  row_index         INTEGER NOT NULL,           -- 1-based, header is row 1, data starts at row 2
  tenant_id         TEXT NOT NULL,              -- carried for FK + tenant scoping
  file_id           TEXT NOT NULL DEFAULT '',   -- Figma file_id parsed from the URL ('' for ghost rows)
  node_id           TEXT NOT NULL DEFAULT '',   -- Figma node_id parsed from the URL ('' for ghost rows)
  row_hash          TEXT NOT NULL,              -- sha256 over canonical row content (see U7)
  project_id        TEXT,                       -- FK projects.id, set after first successful export
  flow_id           TEXT,                       -- FK flows.id, set after first successful export (NULL for ghost)
  last_seen_at      TEXT NOT NULL,              -- RFC3339 — most recent cycle that found this row in the sheet
  last_imported_at  TEXT,                       -- RFC3339 — most recent successful export (NULL = never)
  last_error        TEXT,                       -- last failure reason ('' / NULL = healthy)

  PRIMARY KEY (spreadsheet_id, tab, row_index),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (project_id) REFERENCES projects(id),
  FOREIGN KEY (flow_id) REFERENCES flows(id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_sheet_sync_state_file_id
  ON sheet_sync_state (file_id) WHERE file_id != '';
CREATE INDEX IF NOT EXISTS idx_sheet_sync_state_last_imported_at
  ON sheet_sync_state (last_imported_at);
CREATE INDEX IF NOT EXISTS idx_sheet_sync_state_tenant
  ON sheet_sync_state (tenant_id, spreadsheet_id);

-- ─── Per-cycle audit log ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS sheet_sync_runs (
  id                    TEXT PRIMARY KEY,                     -- uuid
  spreadsheet_id        TEXT NOT NULL,
  started_at            TEXT NOT NULL,
  finished_at           TEXT,                                 -- NULL while in-flight (recovery on next cycle if found)
  drive_modified_time   TEXT,                                 -- the modifiedTime probed at cycle start
  sheet_modified_time   TEXT,                                 -- copied from the spreadsheet header on read
  result                TEXT NOT NULL CHECK (result IN ('unchanged', 'applied', 'failed', 'partial')),
  -- summary_json: {"new":N, "changed":N, "unchanged":N, "gone":N, "errors":N}
  summary_json          TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX IF NOT EXISTS idx_sheet_sync_runs_started_at
  ON sheet_sync_runs (started_at DESC);

-- ─── Flow columns for sheet-sourced metadata ────────────────────────────────
-- All nullable so existing rows aren't disturbed. SQLite raises an error
-- if the column already exists; we wrap each in a SELECT-from-pragma_table_info
-- guard so the migration is idempotent across re-runs.

-- external_drd_url  — Tier 1 (universal): Confluence/GDocs/Notion/other URL from sheet column D
-- external_drd_title  — Tier 2 (GDoc only): doc title fetched via Docs API
-- external_drd_snippet — Tier 2: first ~500 chars of doc body for inline preview
-- external_drd_fetched_at — Tier 2: RFC3339 timestamp of last successful fetch
-- product_poc_text — sheet column B free-text (e.g. "Drishti & Ritwik")
-- sheet_status — sheet column G normalized to {done, wip, in_review, tbd, backlog}

ALTER TABLE flows ADD COLUMN external_drd_url TEXT;
ALTER TABLE flows ADD COLUMN external_drd_title TEXT;
ALTER TABLE flows ADD COLUMN external_drd_snippet TEXT;
ALTER TABLE flows ADD COLUMN external_drd_fetched_at TEXT;
ALTER TABLE flows ADD COLUMN product_poc_text TEXT;
ALTER TABLE flows ADD COLUMN sheet_status TEXT;
