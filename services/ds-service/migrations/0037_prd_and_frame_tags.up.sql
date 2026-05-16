-- 0037_prd_and_frame_tags — U4 of docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md.
--
-- PRD as typed stems (KTD-3). The previous Figma→DRD→PRD authoring flow
-- stored PRDs as prose blobs that every downstream consumer (Storybook,
-- Playwright, Mixpanel, JIRA) had to re-parse from markdown. This
-- migration shapes the PRD as a parent row + child detail rows so each
-- consumer reads its own typed stem.
--
-- Schema shape:
--   prd                            (one per sub_flow)
--   ├─ prd_tab                     (logical tabs — Investment / Banks / Protection / …)
--   │  └─ prd_state                (rows in the PRD's "Possible States" table)
--   │     ├─ prd_state_acceptance_criterion
--   │     ├─ prd_state_edge_case
--   │     ├─ prd_state_copy_string
--   │     ├─ prd_state_event       (Mixpanel tracking-plan; verb taxonomy TODO)
--   │     ├─ prd_state_a11y_note
--   │     └─ frame_tag             (binds the state to Figma frame nodes)
--
-- Binding contract (per Execution Notes §A clarification 2):
--   The designer's frame NAME is canonical. No @role component property is
--   read. `prd_state.frame_name` carries the designer-canonical label used
--   by U2b auto-skeleton; `frame_tag.figma_node_id` is the concrete Figma
--   node reference (no FK because figma_node_metadata lives in a separate
--   data lifecycle — see Execution Notes §B finding 2).
--
-- Mixpanel validation (per Execution Notes §A clarification 4):
--   `prd_state_event.name` has no CHECK or FK. Verb taxonomy is deferred to
--   the analytics team. See `TODO(mixpanel)` comment below.
--
-- Conventions:
--   - STRICT mode on every table (matches recent migrations: 0034, 0036).
--   - ON DELETE CASCADE on tenant FK + parent FK so a sub_flow drop clears
--     the whole subtree.
--   - Composite PK `(tenant_id, id)` mirrors the pattern in 0036_sub_flow.
--   - Partial unique indexes for nullable-unique columns
--     (e.g., prd_state.label is unique per tab only when deleted_at IS NULL).
--   - LOWER(TRIM(name)) indexes match the repo's `normalizeName` lookup key.

-- ─── prd ─────────────────────────────────────────────────────────────────────
-- One PRD per sub_flow. The (tenant_id, sub_flow_id) unique index enforces
-- the 1:1 relationship; UpsertPRD is idempotent on this key.
CREATE TABLE IF NOT EXISTS prd (
    id                TEXT NOT NULL,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sub_flow_id       TEXT NOT NULL,
    title             TEXT NOT NULL DEFAULT '',
    summary_md        TEXT NOT NULL DEFAULT '',
    design_notes_md   TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, sub_flow_id) REFERENCES sub_flow(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prd_tenant_subflow ON prd(tenant_id, sub_flow_id);

-- ─── prd_tab ─────────────────────────────────────────────────────────────────
-- One row per logical tab inside the PRD (Investment / Banks / Protection / …).
-- Case-insensitive uniqueness on the tab name within a PRD; UpsertPRDTab
-- matches by LOWER(TRIM(name)).
CREATE TABLE IF NOT EXISTS prd_tab (
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_id       TEXT NOT NULL,
    name         TEXT NOT NULL,
    position     INTEGER NOT NULL DEFAULT 0,
    overview_md  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_id) REFERENCES prd(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prd_tab_tenant_prd_name_lower
    ON prd_tab(tenant_id, prd_id, LOWER(TRIM(name)));

-- ─── prd_state ───────────────────────────────────────────────────────────────
-- One row per state (one row in the PRD's "Possible States" table).
--
-- frame_name (nullable): the designer-canonical name from Figma, used by
-- U2b auto-skeleton at creation time. NULL means the state was authored
-- before a matching frame exists (PM-led authoring flow).
--
-- deleted_at: soft-delete. U2b clears + re-sets this when a designer
-- removes/restores a frame. Authored content (criteria, events, copy) is
-- preserved across the soft-delete cycle.
--
-- Case-insensitive uniqueness on (prd_tab_id, label) ONLY among live
-- (deleted_at IS NULL) rows. UpsertPRDState matches by this key; re-running
-- it on a soft-deleted row clears deleted_at (idempotent restore).
CREATE TABLE IF NOT EXISTS prd_state (
    id                    TEXT NOT NULL,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_tab_id            TEXT NOT NULL,
    label                 TEXT NOT NULL,
    position              INTEGER NOT NULL DEFAULT 0,
    frame_name            TEXT,                          -- designer-canonical name; NULL when no skeleton match
    condition_md          TEXT NOT NULL DEFAULT '',
    design_handling_md    TEXT NOT NULL DEFAULT '',
    fe_handling_md        TEXT NOT NULL DEFAULT '',
    deleted_at            TEXT,                          -- soft-delete; NULL = live
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_tab_id) REFERENCES prd_tab(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prd_state_tenant_tab_label_lower
    ON prd_state(tenant_id, prd_tab_id, LOWER(TRIM(label))) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_prd_state_tab_position
    ON prd_state(tenant_id, prd_tab_id, position) WHERE deleted_at IS NULL;

-- ─── Typed stems (KTD-3) ─────────────────────────────────────────────────────
-- Every downstream consumer reads its own table, no markdown parsing required.

-- prd_state_acceptance_criterion — feeds Playwright/spec generation.
CREATE TABLE IF NOT EXISTS prd_state_acceptance_criterion (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id   TEXT NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    criterion      TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS idx_prd_state_acceptance_state
    ON prd_state_acceptance_criterion(tenant_id, prd_state_id, position);

-- prd_state_edge_case — informs both QA and Storybook variant matrices.
CREATE TABLE IF NOT EXISTS prd_state_edge_case (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id   TEXT NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    edge_case      TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS idx_prd_state_edge_case_state
    ON prd_state_edge_case(tenant_id, prd_state_id, position);

-- prd_state_copy_string — i18n source of truth.
-- (prd_state_id, key, locale) is unique so UpsertCopyString is idempotent.
CREATE TABLE IF NOT EXISTS prd_state_copy_string (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id   TEXT NOT NULL,
    key            TEXT NOT NULL,
    value          TEXT NOT NULL,
    locale         TEXT NOT NULL DEFAULT 'en',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prd_state_copy_state_key_locale
    ON prd_state_copy_string(tenant_id, prd_state_id, key, locale);

-- prd_state_event — Mixpanel tracking-plan source of truth.
-- TODO(mixpanel): verb taxonomy and validation per analytics team — no CHECK on name yet.
-- (prd_state_id, name) is unique so AddEvent is idempotent.
CREATE TABLE IF NOT EXISTS prd_state_event (
    id                 TEXT NOT NULL,
    tenant_id          TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id       TEXT NOT NULL,
    position           INTEGER NOT NULL DEFAULT 0,
    name               TEXT NOT NULL,                  -- e.g. "wallet.m2m_settlement.cold_state_viewed"
    properties_schema  TEXT NOT NULL DEFAULT '{}',     -- JSON; {prop_name: type, ...}
    fires_on           TEXT NOT NULL DEFAULT '',       -- "user_taps_cta" / "screen_viewed" / "submit_success"
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prd_state_event_state_name
    ON prd_state_event(tenant_id, prd_state_id, name);

-- prd_state_a11y_note — accessibility annotations; surfaced in QA + Storybook docs.
CREATE TABLE IF NOT EXISTS prd_state_a11y_note (
    id             TEXT NOT NULL,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id   TEXT NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    note           TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS idx_prd_state_a11y_state
    ON prd_state_a11y_note(tenant_id, prd_state_id, position);

-- ─── frame_tag ───────────────────────────────────────────────────────────────
-- Which figma frames are bound to this PRD state. variant lets duplicate-
-- name frames (android/ios variants of the same state) attach to the same
-- prd_state without colliding on the unique index.
--
-- figma_node_id has no FK because figma_node_metadata (mig 0034) is
-- currently populated by external Python scripts in /tmp (see Execution
-- Notes §D); cross-tenant data lifecycle would make an FK brittle.
CREATE TABLE IF NOT EXISTS frame_tag (
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id    TEXT NOT NULL,
    figma_node_id   TEXT NOT NULL,                      -- ref to figma_node_metadata.node_id (no FK; cross-tenant data lifecycle)
    variant         TEXT,                                -- "android" / "ios" / "desktop" / NULL
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_frame_tag_state_node_variant
    ON frame_tag(tenant_id, prd_state_id, figma_node_id, COALESCE(variant, ''));
CREATE INDEX IF NOT EXISTS idx_frame_tag_node
    ON frame_tag(tenant_id, figma_node_id);
