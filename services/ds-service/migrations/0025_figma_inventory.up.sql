-- 0025_figma_inventory — Figma DB: team > project > file > page > section
-- inventory mirror (2026-05-13).
--
-- This is metadata only, polled every 5 minutes by internal/figma/inventory.
-- It does NOT store node trees; for that, see screen_canonical_trees (0001).
-- The existing `projects` table is DS-INTERNAL (Networth, INDstocks, etc.)
-- and predates this work — Figma-side projects live here under figma_project
-- to avoid the name collision.
--
-- Two-tier change detection: the poller hits /v1/teams/<id>/projects every
-- 5 minutes, then /v1/projects/<id>/files for each project. The cheap
-- per-file `last_modified` returned by the file-list endpoint is compared
-- to figma_file.last_modified; only files whose timestamp moved get the
-- expensive `/v1/files/<key>?depth=2` call that refreshes pages + sections.
--
-- Soft delete only: when a project/file/page/section disappears from a
-- crawl response, the row is marked `deleted_at` and `last_seen_at` is
-- left at its prior value. No hard deletes — admins want history.

-- ─── seed list ───────────────────────────────────────────────────────────────
-- Figma has no "list my teams" endpoint, so admins paste team IDs here.
-- A row stays enabled until an admin disables it; the poller skips disabled
-- rows but keeps any data already collected.

CREATE TABLE IF NOT EXISTS figma_team_seed (
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    team_id             TEXT NOT NULL,
    team_name           TEXT NOT NULL,         -- admin-supplied label (the API doesn't return team name on /me)
    added_by_user_id    TEXT NOT NULL,
    added_at            TEXT NOT NULL,         -- RFC3339
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    last_crawl_at       TEXT,                  -- RFC3339, NULL until first crawl
    last_crawl_status   TEXT,                  -- 'ok' | 'forbidden' | 'error'
    last_crawl_error    TEXT,                  -- truncated error body for triage
    PRIMARY KEY (tenant_id, team_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_team_seed_enabled
    ON figma_team_seed (tenant_id, enabled);

-- ─── observed team ───────────────────────────────────────────────────────────
-- One row per seeded team after the first crawl. Distinct from figma_team_seed
-- so we can have an observed row even if the seed was later disabled (for
-- audit + dashboard "last known good" rendering).

CREATE TABLE IF NOT EXISTS figma_team (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    team_id          TEXT NOT NULL,
    name             TEXT NOT NULL,
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    PRIMARY KEY (tenant_id, team_id)
) STRICT;

-- ─── projects ────────────────────────────────────────────────────────────────
-- Figma-side projects. One per project under a team. Soft-deleted via
-- deleted_at when the project disappears from the team's project list.

CREATE TABLE IF NOT EXISTS figma_project (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id       TEXT NOT NULL,
    team_id          TEXT NOT NULL,
    name             TEXT NOT NULL,
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    deleted_at       TEXT,
    PRIMARY KEY (tenant_id, project_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_project_team
    ON figma_project (tenant_id, team_id, deleted_at);

-- ─── files ───────────────────────────────────────────────────────────────────
-- One row per file under a project. The cheap-to-refresh fields
-- (last_modified, version, thumbnail_url, name) come from
-- /v1/projects/<id>/files. The expensive fields (pages + sections) are
-- refreshed only when last_modified moves, tracked via pages_sync_version.

CREATE TABLE IF NOT EXISTS figma_file (
    tenant_id            TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key             TEXT NOT NULL,
    project_id           TEXT NOT NULL,
    team_id              TEXT NOT NULL,        -- denorm so dashboard can filter team-wide without join
    name                 TEXT NOT NULL,
    thumbnail_url        TEXT,
    last_modified        TEXT,                 -- RFC3339 from API; drives refetch decision
    version              TEXT,                 -- monotonic-ish version string from /v1/files
    editor_type          TEXT,
    link_access          TEXT,
    role                 TEXT,                 -- PAT's role on the file ('owner'|'editor'|'viewer')
    branch_of_file_key   TEXT,                 -- when this file is a branch; NULL otherwise
    pages_last_synced_at TEXT,                 -- when we last ran the depth=2 fetch
    pages_sync_version   TEXT,                 -- the file.version captured at the last successful pages sync
    first_seen_at        TEXT NOT NULL,
    last_seen_at         TEXT NOT NULL,
    deleted_at           TEXT,
    PRIMARY KEY (tenant_id, file_key)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_file_project
    ON figma_file (tenant_id, project_id, deleted_at);

CREATE INDEX IF NOT EXISTS idx_figma_file_team
    ON figma_file (tenant_id, team_id, deleted_at);

-- The poller scans this index to find files needing a pages-refresh: when
-- last_modified is newer than pages_sync_version (or pages_last_synced_at
-- is NULL, i.e. never synced).
CREATE INDEX IF NOT EXISTS idx_figma_file_needs_pages_sync
    ON figma_file (tenant_id, pages_last_synced_at);

-- ─── pages ───────────────────────────────────────────────────────────────────
-- Top-level CANVAS nodes within a file. Pages have no x/y (the canvas is
-- infinite); order_index is the position in document.children, which is
-- the visual ordering shown in Figma's sidebar.

CREATE TABLE IF NOT EXISTS figma_page (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key         TEXT NOT NULL,
    page_id          TEXT NOT NULL,            -- Figma node id, e.g. "0:1"
    name             TEXT NOT NULL,
    order_index      INTEGER NOT NULL,
    background_color TEXT,                     -- hex like "#ffffff" when present
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    deleted_at       TEXT,
    PRIMARY KEY (tenant_id, file_key, page_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_page_file
    ON figma_page (tenant_id, file_key, deleted_at);

-- ─── sections ────────────────────────────────────────────────────────────────
-- SECTION nodes directly under a page. The Figma plugin spec calls them
-- "sections" — they're visible rectangles drawn on the canvas to group
-- frames. depth=2 from the API returns these as page.children where
-- type == "SECTION". We don't descend into a section's children — the user
-- explicitly scoped this out.

CREATE TABLE IF NOT EXISTS figma_section (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key         TEXT NOT NULL,
    page_id          TEXT NOT NULL,
    section_id       TEXT NOT NULL,
    name             TEXT NOT NULL,
    x                REAL NOT NULL,
    y                REAL NOT NULL,
    width            REAL NOT NULL,
    height           REAL NOT NULL,
    order_index      INTEGER NOT NULL,
    first_seen_at    TEXT NOT NULL,
    last_seen_at     TEXT NOT NULL,
    deleted_at       TEXT,
    PRIMARY KEY (tenant_id, file_key, page_id, section_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_section_page
    ON figma_section (tenant_id, file_key, page_id, deleted_at);

-- ─── observability ───────────────────────────────────────────────────────────
-- One row per poll cycle, per tenant. Lets the admin UI render a "last
-- crawl: N seconds ago — 5 teams, 137 files, 12 refetched, 0 errors" status
-- line and a chart over time.

CREATE TABLE IF NOT EXISTS figma_inventory_run (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,
    teams_crawled       INTEGER NOT NULL DEFAULT 0,
    projects_seen       INTEGER NOT NULL DEFAULT 0,
    files_seen          INTEGER NOT NULL DEFAULT 0,
    files_refetched     INTEGER NOT NULL DEFAULT 0,
    pages_upserted      INTEGER NOT NULL DEFAULT 0,
    sections_upserted   INTEGER NOT NULL DEFAULT 0,
    error_count         INTEGER NOT NULL DEFAULT 0,
    error_sample_json   TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_inventory_run_tenant
    ON figma_inventory_run (tenant_id, started_at DESC);
