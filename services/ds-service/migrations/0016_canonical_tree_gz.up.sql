-- 0016_canonical_tree_gz — T8 (audit follow-up plan 2026-05-03-001).
--
-- Add canonical_tree_gz BLOB so the dereferenced Figma node JSON can be
-- stored gzipped instead of as raw TEXT. Today screen_canonical_trees is
-- 95+ MB on Fly's volume — 97% of the SQLite size — for content the
-- frontend never reads at runtime (it's only the audit core + atlas
-- edge computation that consume canonical trees, both server-side).
-- Gzip on JSON typically gets 5-10x compression; expected steady-state
-- size after backfill: 10-20 MB.
--
-- Strategy: dual-column during the migration, with read paths preferring
-- _gz and falling back to the legacy column for un-backfilled rows.
-- cmd/compress-trees is a one-shot that walks every row, gzips the
-- legacy value into _gz, and nulls out canonical_tree. Once the Fly
-- backfill reports zero remaining rows, a follow-up migration drops
-- the legacy column entirely (out of scope here).

ALTER TABLE screen_canonical_trees ADD COLUMN canonical_tree_gz BLOB;
