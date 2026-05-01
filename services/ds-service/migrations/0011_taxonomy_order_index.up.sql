-- 0011_taxonomy_order_index — Phase 7.6 — drag-to-reorder support.
--
-- canonical_taxonomy was alphabetical-ordered at render time in the
-- v1 (Phase 7.5) curator. Phase 7.6 adds an explicit order_index per
-- (tenant_id, product) sibling group so DS leads can drag-to-reorder
-- folders into the canonical product narrative (e.g. "Onboarding"
-- before "Settings").
--
-- order_index is NOT NULL DEFAULT 0; existing rows keep alphabetical
-- order until someone drags. The reorder handler writes contiguous
-- 0..N values per sibling group on save.

ALTER TABLE canonical_taxonomy ADD COLUMN order_index INTEGER NOT NULL DEFAULT 0;

-- Speed up the per-product ordered fetch the curator UI runs.
CREATE INDEX IF NOT EXISTS idx_canonical_taxonomy_order
    ON canonical_taxonomy(tenant_id, product, order_index)
    WHERE archived_at IS NULL;
