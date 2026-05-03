-- 0015_tenant_fk_constraints — T7 (audit follow-up plan 2026-05-03-001).
--
-- Add FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE to
-- the 11 tables that hold tenant_id today without a database-level
-- constraint. Pre-T7 tenant isolation was application-only — every read
-- query had to remember to filter by tenant_id, and a missed WHERE
-- clause would expose cross-tenant rows with no DB-level guard. With
-- FKs in place, deleting a tenant cascades through every dependent
-- table, and orphaned rows become impossible.
--
-- Mechanics — SQLite doesn't support `ALTER TABLE ADD CONSTRAINT`, so
-- each table requires the rebuild dance: CREATE _new with the new FK,
-- INSERT … SELECT, DROP old, RENAME _new, recreate indexes.
--
-- PRAGMA foreign_keys = OFF disables FK enforcement during the dance
-- (a referenced table is briefly dropped before the RENAME swap; without
-- the OFF the engine fires SQLITE_CONSTRAINT_FOREIGNKEY at INSERT time).
-- This PRAGMA cannot be toggled inside a transaction — hence the .no_tx.
-- naming suffix that the migration runner recognises (internal/db/
-- migrations.go applyOneNoTx). The runner pins a single connection so
-- the pragma persists across statements.
--
-- Pre-migration zero-orphan audit confirmed every existing row's
-- tenant_id matches a real tenant, so foreign_key_check after re-enable
-- returns clean.

PRAGMA foreign_keys = OFF;

BEGIN;

-- ─── flows ──────────────────────────────────────────────────────────────────
CREATE TABLE flows_new (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_id     TEXT NOT NULL,
    section_id  TEXT,
    name        TEXT NOT NULL,
    persona_id  TEXT REFERENCES personas(id) ON DELETE SET NULL,
    deleted_at  TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
INSERT INTO flows_new (id, project_id, tenant_id, file_id, section_id, name, persona_id, deleted_at, created_at, updated_at)
SELECT id, project_id, tenant_id, file_id, section_id, name, persona_id, deleted_at, created_at, updated_at FROM flows;
DROP TABLE flows;
ALTER TABLE flows_new RENAME TO flows;
CREATE INDEX idx_flows_project ON flows(project_id, deleted_at);
CREATE UNIQUE INDEX idx_flows_unique
    ON flows(tenant_id, file_id, section_id, persona_id) WHERE deleted_at IS NULL;

-- ─── project_versions ───────────────────────────────────────────────────────
CREATE TABLE project_versions_new (
    id                       TEXT PRIMARY KEY,
    project_id               TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    tenant_id                TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    version_index            INTEGER NOT NULL,
    status                   TEXT NOT NULL,
    pipeline_started_at      TEXT,
    pipeline_heartbeat_at    TEXT,
    error                    TEXT,
    pruned_at                TEXT,
    created_by_user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at               TEXT NOT NULL
);
INSERT INTO project_versions_new (id, project_id, tenant_id, version_index, status, pipeline_started_at, pipeline_heartbeat_at, error, pruned_at, created_by_user_id, created_at)
SELECT id, project_id, tenant_id, version_index, status, pipeline_started_at, pipeline_heartbeat_at, error, pruned_at, created_by_user_id, created_at FROM project_versions;
DROP TABLE project_versions;
ALTER TABLE project_versions_new RENAME TO project_versions;
CREATE UNIQUE INDEX idx_versions_project_index ON project_versions(project_id, version_index);
CREATE INDEX idx_versions_project_recent ON project_versions(project_id, version_index DESC);
CREATE INDEX idx_versions_status_heartbeat ON project_versions(status, pipeline_heartbeat_at);
CREATE INDEX idx_project_versions_prunable
    ON project_versions(project_id, version_index)
    WHERE pruned_at IS NULL AND status IN ('view_ready', 'failed');

-- ─── screens ────────────────────────────────────────────────────────────────
CREATE TABLE screens_new (
    id                  TEXT PRIMARY KEY,
    version_id          TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    flow_id             TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    x                   REAL NOT NULL,
    y                   REAL NOT NULL,
    width               REAL NOT NULL,
    height              REAL NOT NULL,
    screen_logical_id   TEXT NOT NULL,
    png_storage_key     TEXT,
    created_at          TEXT NOT NULL
);
INSERT INTO screens_new (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, png_storage_key, created_at)
SELECT id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, png_storage_key, created_at FROM screens;
DROP TABLE screens;
ALTER TABLE screens_new RENAME TO screens;
CREATE INDEX idx_screens_flow_logical ON screens(flow_id, screen_logical_id);
CREATE INDEX idx_screens_version ON screens(version_id);

-- ─── violations ─────────────────────────────────────────────────────────────
CREATE TABLE violations_new (
    id            TEXT PRIMARY KEY,
    version_id    TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    screen_id     TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id       TEXT NOT NULL,
    severity      TEXT NOT NULL,
    property      TEXT NOT NULL,
    observed      TEXT,
    suggestion    TEXT,
    persona_id    TEXT REFERENCES personas(id) ON DELETE SET NULL,
    mode_label    TEXT,
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT 'token_drift',
    auto_fixable  INTEGER NOT NULL DEFAULT 0
);
INSERT INTO violations_new (id, version_id, screen_id, tenant_id, rule_id, severity, property, observed, suggestion, persona_id, mode_label, status, created_at, category, auto_fixable)
SELECT id, version_id, screen_id, tenant_id, rule_id, severity, property, observed, suggestion, persona_id, mode_label, status, created_at, category, auto_fixable FROM violations;
DROP TABLE violations;
ALTER TABLE violations_new RENAME TO violations;
CREATE INDEX idx_violations_inbox ON violations(tenant_id, status, severity, created_at DESC);
CREATE INDEX idx_violations_screen ON violations(screen_id);
CREATE INDEX idx_violations_screen_category ON violations(screen_id, category);
CREATE INDEX idx_violations_version_category ON violations(version_id, category);
CREATE INDEX idx_violations_version_severity ON violations(version_id, severity);

-- ─── audit_jobs ─────────────────────────────────────────────────────────────
CREATE TABLE audit_jobs_new (
    id                 TEXT PRIMARY KEY,
    version_id         TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    status             TEXT NOT NULL,
    trace_id           TEXT NOT NULL,
    idempotency_key    TEXT NOT NULL,
    leased_by          TEXT,
    lease_expires_at   INTEGER,
    created_at         TEXT NOT NULL,
    started_at         TEXT,
    completed_at       TEXT,
    error              TEXT,
    priority           INTEGER NOT NULL DEFAULT 50,
    triggered_by       TEXT NOT NULL DEFAULT 'export',
    metadata           TEXT
);
INSERT INTO audit_jobs_new (id, version_id, tenant_id, status, trace_id, idempotency_key, leased_by, lease_expires_at, created_at, started_at, completed_at, error, priority, triggered_by, metadata)
SELECT id, version_id, tenant_id, status, trace_id, idempotency_key, leased_by, lease_expires_at, created_at, started_at, completed_at, error, priority, triggered_by, metadata FROM audit_jobs;
DROP TABLE audit_jobs;
ALTER TABLE audit_jobs_new RENAME TO audit_jobs;
CREATE INDEX idx_audit_jobs_status_created ON audit_jobs(status, created_at);
CREATE INDEX idx_audit_jobs_status_priority_created ON audit_jobs(status, priority DESC, created_at);
CREATE INDEX idx_audit_jobs_trace ON audit_jobs(trace_id);
CREATE INDEX idx_audit_jobs_triggered_by ON audit_jobs(triggered_by, created_at);
CREATE UNIQUE INDEX idx_audit_jobs_version_active ON audit_jobs(version_id) WHERE status IN ('queued','running');

-- ─── decisions ──────────────────────────────────────────────────────────────
CREATE TABLE decisions_new (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    flow_id            TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    version_id         TEXT NOT NULL REFERENCES project_versions(id) ON DELETE RESTRICT,
    title              TEXT NOT NULL,
    body_json          BLOB,
    status             TEXT NOT NULL DEFAULT 'accepted',
    made_by_user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    made_at            TEXT NOT NULL,
    superseded_by_id   TEXT REFERENCES decisions(id) ON DELETE SET NULL,
    supersedes_id      TEXT REFERENCES decisions(id) ON DELETE SET NULL,
    deleted_at         TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
INSERT INTO decisions_new (id, tenant_id, flow_id, version_id, title, body_json, status, made_by_user_id, made_at, superseded_by_id, supersedes_id, deleted_at, created_at, updated_at)
SELECT id, tenant_id, flow_id, version_id, title, body_json, status, made_by_user_id, made_at, superseded_by_id, supersedes_id, deleted_at, created_at, updated_at FROM decisions;
DROP TABLE decisions;
ALTER TABLE decisions_new RENAME TO decisions;
CREATE INDEX idx_decisions_chain ON decisions(supersedes_id) WHERE supersedes_id IS NOT NULL;
CREATE INDEX idx_decisions_flow_made_at ON decisions(tenant_id, flow_id, made_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_decisions_recent ON decisions(made_at DESC) WHERE deleted_at IS NULL AND status IN ('proposed', 'accepted');

-- ─── drd_comments ───────────────────────────────────────────────────────────
CREATE TABLE drd_comments_new (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    target_kind        TEXT NOT NULL,
    target_id          TEXT NOT NULL,
    flow_id            TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    author_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    body               TEXT NOT NULL,
    parent_comment_id  TEXT REFERENCES drd_comments(id) ON DELETE SET NULL,
    mentions_user_ids  TEXT,
    resolved_at        TEXT,
    resolved_by        TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    deleted_at         TEXT
);
INSERT INTO drd_comments_new (id, tenant_id, target_kind, target_id, flow_id, author_user_id, body, parent_comment_id, mentions_user_ids, resolved_at, resolved_by, created_at, updated_at, deleted_at)
SELECT id, tenant_id, target_kind, target_id, flow_id, author_user_id, body, parent_comment_id, mentions_user_ids, resolved_at, resolved_by, created_at, updated_at, deleted_at FROM drd_comments;
DROP TABLE drd_comments;
ALTER TABLE drd_comments_new RENAME TO drd_comments;
CREATE INDEX idx_drd_comments_author ON drd_comments(author_user_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_drd_comments_flow ON drd_comments(tenant_id, flow_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_drd_comments_target ON drd_comments(tenant_id, target_kind, target_id, created_at) WHERE deleted_at IS NULL;

-- ─── screen_modes ───────────────────────────────────────────────────────────
CREATE TABLE screen_modes_new (
    id                            TEXT PRIMARY KEY,
    screen_id                     TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id                     TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mode_label                    TEXT NOT NULL,
    figma_frame_id                TEXT NOT NULL,
    explicit_variable_modes_json  TEXT
);
INSERT INTO screen_modes_new (id, screen_id, tenant_id, mode_label, figma_frame_id, explicit_variable_modes_json)
SELECT id, screen_id, tenant_id, mode_label, figma_frame_id, explicit_variable_modes_json FROM screen_modes;
DROP TABLE screen_modes;
ALTER TABLE screen_modes_new RENAME TO screen_modes;
CREATE UNIQUE INDEX idx_screen_modes_unique ON screen_modes(screen_id, mode_label);

-- ─── flow_drd ───────────────────────────────────────────────────────────────
CREATE TABLE flow_drd_new (
    flow_id              TEXT PRIMARY KEY REFERENCES flows(id) ON DELETE RESTRICT,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    content_json         BLOB NOT NULL,
    revision             INTEGER NOT NULL DEFAULT 0,
    schema_version       TEXT,
    updated_at           TEXT NOT NULL,
    updated_by_user_id   TEXT REFERENCES users(id) ON DELETE SET NULL,
    y_doc_state          BLOB,
    last_snapshot_at     TEXT
);
INSERT INTO flow_drd_new (flow_id, tenant_id, content_json, revision, schema_version, updated_at, updated_by_user_id, y_doc_state, last_snapshot_at)
SELECT flow_id, tenant_id, content_json, revision, schema_version, updated_at, updated_by_user_id, y_doc_state, last_snapshot_at FROM flow_drd;
DROP TABLE flow_drd;
ALTER TABLE flow_drd_new RENAME TO flow_drd;

-- ─── screen_prototype_links ─────────────────────────────────────────────────
CREATE TABLE screen_prototype_links_new (
    id                      TEXT PRIMARY KEY,
    screen_id               TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_node_id          TEXT NOT NULL,
    destination_screen_id   TEXT REFERENCES screens(id) ON DELETE SET NULL,
    destination_node_id     TEXT,
    trigger                 TEXT NOT NULL,
    action                  TEXT NOT NULL,
    metadata                TEXT,
    created_at              TEXT NOT NULL
);
INSERT INTO screen_prototype_links_new (id, screen_id, tenant_id, source_node_id, destination_screen_id, destination_node_id, trigger, action, metadata, created_at)
SELECT id, screen_id, tenant_id, source_node_id, destination_screen_id, destination_node_id, trigger, action, metadata, created_at FROM screen_prototype_links;
DROP TABLE screen_prototype_links;
ALTER TABLE screen_prototype_links_new RENAME TO screen_prototype_links;
CREATE INDEX idx_proto_links_destination ON screen_prototype_links(tenant_id, destination_screen_id);
CREATE INDEX idx_proto_links_screen ON screen_prototype_links(screen_id);
CREATE INDEX idx_proto_links_source ON screen_prototype_links(tenant_id, source_node_id);

-- ─── flow_grants ────────────────────────────────────────────────────────────
CREATE TABLE flow_grants_new (
    flow_id     TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    granted_by  TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at  TEXT NOT NULL,
    revoked_at  TEXT,
    PRIMARY KEY (flow_id, user_id)
);
INSERT INTO flow_grants_new (flow_id, user_id, tenant_id, role, granted_by, granted_at, revoked_at)
SELECT flow_id, user_id, tenant_id, role, granted_by, granted_at, revoked_at FROM flow_grants;
DROP TABLE flow_grants;
ALTER TABLE flow_grants_new RENAME TO flow_grants;
CREATE INDEX idx_flow_grants_flow ON flow_grants(flow_id, user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_flow_grants_user ON flow_grants(user_id, flow_id) WHERE revoked_at IS NULL;

COMMIT;

PRAGMA foreign_keys = ON;
PRAGMA foreign_key_check;
