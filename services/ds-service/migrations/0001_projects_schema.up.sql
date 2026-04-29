-- 0001_projects_schema — Projects · Flow Atlas Phase 1 schema
--
-- Adds the 10 tables Phase 1 needs, with explicit foreign keys, cascade rules,
-- denormalized tenant_id (for query-time scoping by TenantRepo), soft-delete
-- columns, NOT NULL specs, and UNIQUE constraints. Mirrors decisions from
-- docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md U1.
--
-- Foreign-key enforcement requires `PRAGMA foreign_keys = ON` per connection
-- (set in internal/db/db.go's DSN).
--
-- Forward-only column-add discipline applies from this migration onward:
--   - Always ADD COLUMN ... NULL on extensions; never NOT NULL without DEFAULT.
--   - Never DROP COLUMN within the same release that stops writing it.
--   - Renames are 3-release: dual-write → cutover → drop-old.

-- ─── personas ─────────────────────────────────────────────────────────────────
-- Org-wide library; tenant-scoped via tenant_id. status='approved' personas are
-- visible to every designer in the tenant; 'pending' visible only to the
-- suggesting designer + DS leads + admins until approved (Phase 7 admin UI).
CREATE TABLE IF NOT EXISTS personas (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending', -- approved | pending
    created_by_user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    approved_by_user_id   TEXT REFERENCES users(id) ON DELETE SET NULL,
    approved_at           TEXT,
    deleted_at            TEXT,
    created_at            TEXT NOT NULL
);
-- Approved personas are unique per tenant. Pending personas can dupe (multiple
-- designers may suggest the same name; reconciled at approval time).
CREATE UNIQUE INDEX IF NOT EXISTS idx_personas_unique_approved
    ON personas(tenant_id, name) WHERE status = 'approved' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_personas_tenant_status
    ON personas(tenant_id, status, deleted_at);

-- ─── projects ────────────────────────────────────────────────────────────────
-- One row per (tenant, product, platform, path). The product taxonomy is the
-- 9 origin products (Plutus / Tax / Indian Stocks / etc.) curated by DS lead.
CREATE TABLE IF NOT EXISTS projects (
    id            TEXT PRIMARY KEY,
    slug          TEXT NOT NULL,
    name          TEXT NOT NULL,
    platform      TEXT NOT NULL,                -- mobile | web
    product       TEXT NOT NULL,                -- Plutus | Tax | Indian Stocks | …
    path          TEXT NOT NULL,                -- "Indian Stocks/F&O/Learn Touchpoints"
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
-- Slugs are tenant-scoped — "onboarding" can mean different projects in
-- different tenants. Cross-tenant collisions are not collisions.
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_tenant_slug
    ON projects(tenant_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_tenant_active
    ON projects(tenant_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_projects_tenant_product_path
    ON projects(tenant_id, product, path) WHERE deleted_at IS NULL;

-- ─── flows ────────────────────────────────────────────────────────────────────
-- One flow = one Figma section per persona. Re-export keyed by
-- (tenant_id, file_id, section_id, persona_id) — see U4 idempotency.
CREATE TABLE IF NOT EXISTS flows (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    tenant_id   TEXT NOT NULL,
    file_id     TEXT NOT NULL,
    section_id  TEXT,                           -- NULL when freeform (no section ancestor)
    name        TEXT NOT NULL,
    persona_id  TEXT REFERENCES personas(id) ON DELETE SET NULL,
    deleted_at  TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
-- Re-export resolution. Tenant-scoped; section_id may be NULL (uses literal NULL
-- in the unique index — SQLite treats NULLs as distinct, so freeform flows
-- never collide on the section axis).
CREATE UNIQUE INDEX IF NOT EXISTS idx_flows_unique
    ON flows(tenant_id, file_id, section_id, persona_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_flows_project ON flows(project_id, deleted_at);

-- ─── project_versions ────────────────────────────────────────────────────────
-- Each export = one version. Status collapses to pending | view_ready | failed
-- (audit lifecycle lives in audit_jobs only — no denormalized state drift).
-- pipeline_heartbeat_at supports the RecoverStuckVersions sweeper.
CREATE TABLE IF NOT EXISTS project_versions (
    id                       TEXT PRIMARY KEY,
    project_id               TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    tenant_id                TEXT NOT NULL,
    version_index            INTEGER NOT NULL,
    status                   TEXT NOT NULL,     -- pending | view_ready | failed
    pipeline_started_at      TEXT,
    pipeline_heartbeat_at    TEXT,
    error                    TEXT,
    created_by_user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at               TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_versions_project_index
    ON project_versions(project_id, version_index);
CREATE INDEX IF NOT EXISTS idx_versions_project_recent
    ON project_versions(project_id, version_index DESC);
CREATE INDEX IF NOT EXISTS idx_versions_status_heartbeat
    ON project_versions(status, pipeline_heartbeat_at);

-- ─── screens ─────────────────────────────────────────────────────────────────
-- One row per Figma frame in a version. screen_logical_id is stable across
-- re-exports of the same frame within a flow; Phase 1 sets but doesn't read it,
-- Phase 4/5 cross-version refs depend on it.
CREATE TABLE IF NOT EXISTS screens (
    id                  TEXT PRIMARY KEY,
    version_id          TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    flow_id             TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    tenant_id           TEXT NOT NULL,
    x                   REAL NOT NULL,
    y                   REAL NOT NULL,
    width               REAL NOT NULL,
    height              REAL NOT NULL,
    screen_logical_id   TEXT NOT NULL,           -- stable across re-export of same frame
    png_storage_key     TEXT,                    -- relative path under data/screens/
    created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_screens_version ON screens(version_id);
CREATE INDEX IF NOT EXISTS idx_screens_flow_logical ON screens(flow_id, screen_logical_id);

-- ─── screen_canonical_trees ──────────────────────────────────────────────────
-- Lazy-fetched by JSON tab; split out so atlas list queries don't pull MB of
-- JSON the UI doesn't need at list time. Hash column enables Phase 2 dedup.
CREATE TABLE IF NOT EXISTS screen_canonical_trees (
    screen_id        TEXT PRIMARY KEY REFERENCES screens(id) ON DELETE CASCADE,
    canonical_tree   TEXT NOT NULL,
    hash             TEXT,
    updated_at       TEXT NOT NULL
);

-- ─── screen_modes ────────────────────────────────────────────────────────────
-- One row per (screen, mode_label). Phase 1 detects mode pairs but does not
-- persist a theme_parity_warning column — Phase 2 audit recomputes from
-- canonical_trees on demand to avoid stale flags.
CREATE TABLE IF NOT EXISTS screen_modes (
    id                            TEXT PRIMARY KEY,
    screen_id                     TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id                     TEXT NOT NULL,
    mode_label                    TEXT NOT NULL,            -- light | dark | default | …
    figma_frame_id                TEXT NOT NULL,
    explicit_variable_modes_json  TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_screen_modes_unique
    ON screen_modes(screen_id, mode_label);

-- ─── audit_jobs ──────────────────────────────────────────────────────────────
-- Worker pool of size 1 in Phase 1 (Phase 2 grows the constant). Lease columns
-- ship from day one so Phase 2 can introduce concurrent workers without
-- another schema migration.
CREATE TABLE IF NOT EXISTS audit_jobs (
    id                  TEXT PRIMARY KEY,
    version_id          TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    tenant_id           TEXT NOT NULL,
    status              TEXT NOT NULL,                       -- queued | running | done | failed
    trace_id            TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    leased_by           TEXT,
    lease_expires_at    INTEGER,
    created_at          TEXT NOT NULL,
    started_at          TEXT,
    completed_at        TEXT,
    error               TEXT
);
-- Only one active job per version. The partial unique index allows historical
-- 'done' / 'failed' rows to coexist with a new 'queued' row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_jobs_version_active
    ON audit_jobs(version_id) WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS idx_audit_jobs_status_created
    ON audit_jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_jobs_trace ON audit_jobs(trace_id);

-- ─── violations ──────────────────────────────────────────────────────────────
-- Worker writes violations idempotently per version: DELETE WHERE version_id +
-- INSERT new rows in single transaction. 5-tier severity. status lifecycle
-- (active → acknowledged | dismissed | fixed) ships in Phase 4.
CREATE TABLE IF NOT EXISTS violations (
    id           TEXT PRIMARY KEY,
    version_id   TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    screen_id    TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id    TEXT NOT NULL,
    rule_id      TEXT NOT NULL,
    severity     TEXT NOT NULL,    -- critical | high | medium | low | info
    property     TEXT NOT NULL,
    observed     TEXT,
    suggestion   TEXT,
    persona_id   TEXT REFERENCES personas(id) ON DELETE SET NULL,
    mode_label   TEXT,
    status       TEXT NOT NULL DEFAULT 'active', -- active | acknowledged | dismissed | fixed
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_violations_version_severity
    ON violations(version_id, severity);
CREATE INDEX IF NOT EXISTS idx_violations_screen ON violations(screen_id);

-- ─── flow_drd ────────────────────────────────────────────────────────────────
-- One DRD per flow, living. revision is a monotonic counter for ETag-style
-- optimistic concurrency (SQLite CURRENT_TIMESTAMP is 1-second resolution and
-- silently overwrites within that window — revision avoids it).
CREATE TABLE IF NOT EXISTS flow_drd (
    flow_id              TEXT PRIMARY KEY REFERENCES flows(id) ON DELETE RESTRICT,
    tenant_id            TEXT NOT NULL,
    content_json         BLOB NOT NULL,
    revision             INTEGER NOT NULL DEFAULT 0,
    schema_version       TEXT,
    updated_at           TEXT NOT NULL,
    updated_by_user_id   TEXT REFERENCES users(id) ON DELETE SET NULL
);
