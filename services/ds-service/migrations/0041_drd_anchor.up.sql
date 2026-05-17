-- 0041_drd_anchor.up.sql — Plan 005 Phase B.
--
-- Anchors that bind a DRD BlockNote block to a prototype screen so the
-- Atlas PrototypeAnchorBridge can deterministically resolve a
-- screen-click → DRD-block scroll. Without this the bridge falls back
-- to a heuristic (label substring + heading bias), which lands on the
-- wrong block when the DRD has multiple paragraphs mentioning the same
-- term.
--
-- Many-to-many on purpose:
--   - A single DRD block can anchor multiple screens (e.g. the "Trader
--     Mode" heading anchors both S3 and S7).
--   - A single screen can be anchored by multiple blocks (a heading +
--     an acceptance-criteria paragraph that both describe the screen).
-- Uniqueness on (tenant_id, sub_flow_id, block_id, screen_id) prevents
-- duplicates from a slash-command double-click.
--
-- block_id is BlockNote's block UUID — stable across edits because
-- BlockNote preserves the id when content changes. Stored as TEXT so a
-- future schema can swap to ULIDs without a column migration.

CREATE TABLE IF NOT EXISTS drd_anchor (
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sub_flow_id     TEXT NOT NULL,
    block_id        TEXT NOT NULL,
    screen_id       TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    created_by      TEXT,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, sub_flow_id) REFERENCES sub_flow(tenant_id, id) ON DELETE CASCADE
) STRICT;

-- Forward lookup: "what's the block id for screen Sx in sub_flow Y?"
-- (Atlas bridge reads this on click.)
CREATE INDEX IF NOT EXISTS idx_drd_anchor_subflow_screen
    ON drd_anchor(tenant_id, sub_flow_id, screen_id);

-- Reverse lookup: "what screens anchor to this block?" — used to render
-- the chip badge next to anchored DRD blocks.
CREATE INDEX IF NOT EXISTS idx_drd_anchor_subflow_block
    ON drd_anchor(tenant_id, sub_flow_id, block_id);

-- Idempotency guard. A slash-command double-trigger or a re-run of the
-- /ind-prd seed should not duplicate the anchor.
CREATE UNIQUE INDEX IF NOT EXISTS idx_drd_anchor_unique
    ON drd_anchor(tenant_id, sub_flow_id, block_id, screen_id);
