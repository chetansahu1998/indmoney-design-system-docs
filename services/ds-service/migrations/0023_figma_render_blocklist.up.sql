-- 0023_figma_render_blocklist — known-bad-frame skip list (2026-05-12).
--
-- Problem: Figma's /v1/images endpoint deterministically fails on a small
-- set of frames per file ("figma rendered no URL for frame X"). Without
-- memory, every sync cycle + Stage 9 pre-render + on-demand fetch
-- re-attempts the same frame, burns the per-PAT rate-limit budget, and
-- still fails. Observed in goals-revamp-iteration-2 (frame 782:89312),
-- indstocks-masthead-and-navigation-indstocks (1814:584609), and ~5%
-- of plutus-equity-tracking clusters during the 2026-05-09–11 audit.
--
-- This table records persistent failures with a cooldown so callers can
-- short-circuit before calling Figma. The skip is hash-keyed: if the
-- canonical_tree for the screen referencing this node changes (the
-- designer edited the frame in Figma), `clear_hash` invalidates the
-- blocklist entry on next sync — their edit may have resolved the
-- upstream render bug.
--
-- Composite primary key (tenant_id, file_id, node_id) means a node that
-- appears in multiple tenants stays scoped per-tenant — Figma's render
-- bugs are per-(file, node) and don't cross tenants, but the key shape
-- preserves the existing tenant-scoping convention used elsewhere.

CREATE TABLE IF NOT EXISTS figma_render_blocklist (
    tenant_id              TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_id                TEXT NOT NULL,
    node_id                TEXT NOT NULL,
    -- ISO-8601 UTC timestamps for full ordering.
    first_failure_at       TEXT NOT NULL,
    last_failure_at        TEXT NOT NULL,
    consecutive_failures   INTEGER NOT NULL CHECK (consecutive_failures > 0),
    last_error             TEXT NOT NULL,
    -- After this instant the entry is "stale" and callers SHOULD re-attempt
    -- once. A successful re-attempt deletes the row; another failure
    -- replaces it with a fresh entry (consecutive_failures resets to 1 OR
    -- accumulates, depending on whether the previous failure was inside
    -- or outside the cooldown — see MarkFigmaRenderFailure).
    cooldown_until         TEXT NOT NULL,
    -- canonical_tree hash of the screen this node belongs to at the time
    -- the blocklist row was inserted. When the next sync produces a tree
    -- with a different hash (designer edited the file), we treat that as
    -- a signal the underlying frame MAY have changed and clear the row
    -- so the next render attempt isn't artificially suppressed.
    clear_hash             TEXT,
    PRIMARY KEY (tenant_id, file_id, node_id)
);

CREATE INDEX IF NOT EXISTS idx_blocklist_cooldown
    ON figma_render_blocklist(cooldown_until);

CREATE INDEX IF NOT EXISTS idx_blocklist_tenant
    ON figma_render_blocklist(tenant_id, last_failure_at);
