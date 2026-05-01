-- 0008_decisions_comments_notifications — Phase 5 U2 schema.
--
-- Phase 5 turns the DRD from a single-author skeleton into a multi-user
-- collaboration surface with first-class Decisions, inline comment threads,
-- @mentions, and a notification fan-out. This migration adds the four
-- entities + extends flow_drd with the Yjs binary state slot.
--
-- Forward-only column-add discipline (per 0001 conventions):
--   - ADD COLUMN ... NULL on extensions; never NOT NULL without DEFAULT.
--   - Never DROP COLUMN within the release that stops writing it.
--
-- Cross-cutting:
--   - Every entity carries denormalized tenant_id so TenantRepo can scope
--     reads by indexed predicate without JOINing back to the parent.
--   - Soft-delete via deleted_at on tables where designers may regret
--     destructive actions (decisions, comments). Notifications are hard-
--     deleted by the digest cleanup job (Phase 7 admin polish; until
--     then they accumulate — bounded by user activity).

-- ─── flow_drd: y_doc_state extension ────────────────────────────────────────
-- Phase 5 U1 — Yjs binary update is the source of truth for live editors.
-- The existing content_json stays as the snapshot REST clients read; on
-- every Y.Doc snapshot persistence Hocuspocus also re-renders the BlockNote
-- JSON for legacy readers (programmatic exporters, e2e tests). last_snapshot_at
-- tracks freshness; revision continues to bump per snapshot so the existing
-- ETag-style optimistic-concurrency contract survives.
ALTER TABLE flow_drd ADD COLUMN y_doc_state BLOB;
ALTER TABLE flow_drd ADD COLUMN last_snapshot_at TEXT;

-- ─── decisions ──────────────────────────────────────────────────────────────
-- One row per Decision entity. Decisions are flow-scoped + version-anchored
-- (a Decision made on v1 stays attached to v1 even after v2 ships).
-- Status enum: proposed | accepted | superseded. Default 'accepted' — the
-- common case is a designer typing /decision in a review meeting after
-- agreement, not before. Downgrade to 'proposed' or supersede later.
CREATE TABLE IF NOT EXISTS decisions (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL,
    flow_id             TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    version_id          TEXT NOT NULL REFERENCES project_versions(id) ON DELETE RESTRICT,
    title               TEXT NOT NULL,
    body_json           BLOB,                               -- BlockNote JSON; nullable for header-only decisions
    status              TEXT NOT NULL DEFAULT 'accepted',   -- proposed | accepted | superseded
    made_by_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    made_at             TEXT NOT NULL,
    superseded_by_id    TEXT REFERENCES decisions(id) ON DELETE SET NULL,
    supersedes_id       TEXT REFERENCES decisions(id) ON DELETE SET NULL,
    deleted_at          TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
-- Listing decisions for a flow's tab is the most common read path.
CREATE INDEX IF NOT EXISTS idx_decisions_flow_made_at
    ON decisions(tenant_id, flow_id, made_at DESC) WHERE deleted_at IS NULL;
-- Recent-decisions feed on /atlas/admin (super-admin scope).
CREATE INDEX IF NOT EXISTS idx_decisions_recent
    ON decisions(made_at DESC) WHERE deleted_at IS NULL AND status IN ('proposed', 'accepted');
-- Supersession-chain walk uses superseded_by_id pointers.
CREATE INDEX IF NOT EXISTS idx_decisions_chain
    ON decisions(supersedes_id) WHERE supersedes_id IS NOT NULL;

-- ─── decision_links ─────────────────────────────────────────────────────────
-- Many-to-many between decisions and target entities. The brainstorm calls
-- for links_to_components + links_to_screens; we generalize so violation
-- links + external-URL links + future entity types all share one schema.
--
-- link_type discriminates the target_id namespace:
--   violation   → violations.id (Phase 4 acknowledge/dismiss-with-decision)
--   screen      → screens.id (decision pinned to a specific screen)
--   component   → component-name string (manifest-resolved client-side)
--   external    → URL string (external ticket / Linear / Slack thread)
CREATE TABLE IF NOT EXISTS decision_links (
    decision_id   TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    link_type     TEXT NOT NULL,           -- violation | screen | component | external
    target_id     TEXT NOT NULL,
    tenant_id     TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    PRIMARY KEY (decision_id, link_type, target_id)
);
-- Reverse lookup: "what decisions reference this violation?" — used by
-- the Violations tab + per-component reverse view.
CREATE INDEX IF NOT EXISTS idx_decision_links_target
    ON decision_links(target_id, link_type, tenant_id);

-- ─── drd_comments ───────────────────────────────────────────────────────────
-- Universal comment table. target_kind discriminates which entity the
-- comment hangs off; target_id is the entity's primary key (or for
-- 'drd_block' the BlockNote block UUID — stable across edits but only
-- valid for the current Y.Doc state).
--
-- Threading is depth=1 in v1: replies set parent_comment_id to the root
-- comment's id; reply-to-reply collapses to the same root. Deeper
-- threading is data-model-compatible (just walk parents); UI ships
-- linear in v1 to ship faster.
--
-- mentions_user_ids is the parsed @mention payload, stored as a JSON
-- array of user_ids. Persisted at write time so notification fan-out is
-- a single-table scan.
CREATE TABLE IF NOT EXISTS drd_comments (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL,
    target_kind        TEXT NOT NULL,                     -- drd_block | decision | violation | screen | comment
    target_id          TEXT NOT NULL,
    flow_id            TEXT NOT NULL REFERENCES flows(id) ON DELETE RESTRICT,
    author_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    body               TEXT NOT NULL,                     -- BlockNote JSON or plain text
    parent_comment_id  TEXT REFERENCES drd_comments(id) ON DELETE SET NULL,
    mentions_user_ids  TEXT,                              -- JSON array of user_ids; NULL when no mentions
    resolved_at        TEXT,
    resolved_by        TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    deleted_at         TEXT
);
-- Render path: comments for a target (drd_block / decision / violation), oldest first.
CREATE INDEX IF NOT EXISTS idx_drd_comments_target
    ON drd_comments(tenant_id, target_kind, target_id, created_at) WHERE deleted_at IS NULL;
-- Author's recent comments (activity rail, Phase 5 U12).
CREATE INDEX IF NOT EXISTS idx_drd_comments_author
    ON drd_comments(author_user_id, created_at DESC) WHERE deleted_at IS NULL;
-- Per-flow comment activity (DRD activity rail).
CREATE INDEX IF NOT EXISTS idx_drd_comments_flow
    ON drd_comments(tenant_id, flow_id, created_at DESC) WHERE deleted_at IS NULL;

-- ─── notifications ──────────────────────────────────────────────────────────
-- Per-recipient notification rows. Reads the same SSE channel
-- (inbox:<tenant_id>) as Phase 4 lifecycle events; the inbox UI's
-- "Mentions" filter chip surfaces these.
--
-- payload_json carries kind-specific fields the UI needs to render
-- without a follow-up fetch (e.g., for kind=mention: comment body
-- snippet, flow_id, target_kind, target_id).
--
-- delivered_via tracks which channels have already shipped the event so
-- the digest worker doesn't re-deliver. JSON array; appended-to-only.
CREATE TABLE IF NOT EXISTS notifications (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL,
    recipient_user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind                 TEXT NOT NULL,                   -- mention | decision_made | decision_superseded | comment_resolved | drd_edited_on_owned_flow
    target_kind          TEXT,                            -- comment | decision | drd | violation
    target_id            TEXT,
    flow_id              TEXT REFERENCES flows(id) ON DELETE SET NULL,
    actor_user_id        TEXT REFERENCES users(id) ON DELETE SET NULL,
    payload_json         TEXT,                            -- kind-specific JSON
    delivered_via        TEXT,                            -- JSON array; e.g. ["in_app","slack"]
    read_at              TEXT,
    created_at           TEXT NOT NULL
);
-- Inbox unread query: scoped by recipient + read_at IS NULL ordered by
-- created_at DESC. Partial index covers the unread tail efficiently.
CREATE INDEX IF NOT EXISTS idx_notifications_inbox_unread
    ON notifications(recipient_user_id, created_at DESC) WHERE read_at IS NULL;
-- Inbox all (read + unread) — partitioned by recipient + tenant for
-- security defense in depth.
CREATE INDEX IF NOT EXISTS idx_notifications_inbox
    ON notifications(tenant_id, recipient_user_id, created_at DESC);
-- Digest worker: scoped by recipient + delivered_via IS NULL/missing.
CREATE INDEX IF NOT EXISTS idx_notifications_digest
    ON notifications(recipient_user_id, created_at) WHERE delivered_via IS NULL;

-- ─── notification_preferences ───────────────────────────────────────────────
-- Per-user, per-channel digest opt-in. cadence='off' (default) means no
-- digest delivery — in-app inbox still surfaces the row. 'daily' delivers
-- once at 09:00 user-local; 'weekly' delivers Monday 09:00 user-local.
--
-- slack_webhook_url + email_address are the per-user delivery targets.
-- Storing the Slack webhook in plaintext is acceptable for v1 because
-- Slack treats webhook URLs as bearer credentials and our DB is at-rest
-- encrypted. Phase 7 polish swaps for an encrypted column matching the
-- figma_tokens pattern.
--
-- Unique on (user_id, channel) so a user has one row per channel.
CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel              TEXT NOT NULL,                   -- slack | email
    cadence              TEXT NOT NULL DEFAULT 'off',     -- off | daily | weekly
    slack_webhook_url    TEXT,
    email_address        TEXT,
    user_tz              TEXT,                            -- IANA tz, e.g. "Asia/Kolkata"
    last_digest_at       TEXT,
    updated_at           TEXT NOT NULL,
    PRIMARY KEY (user_id, channel)
);
