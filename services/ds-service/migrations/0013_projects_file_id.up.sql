-- 0013_projects_file_id — T5 (audit follow-up plan 2026-05-03-001).
--
-- Add `file_id` to projects so two different Figma files can never collapse
-- into one project row. Pre-T5 UpsertProject keyed on
-- (tenant_id, product, platform, path) — three hand-typed strings. A
-- designer who exported "Indian Stocks → research" from File A and then
-- exported a totally different "Indian Stocks → research" from File B
-- silently overwrote the first project's metadata. Frames + flows from
-- File B got nested under File A's project row, with no UI signal that
-- two files had been merged.
--
-- Adding file_id (Figma file key, opaque from the plugin's perspective)
-- as part of the lookup key means each Figma file maps to exactly one
-- project row per tenant. Re-exports from the same file UPSERT the same
-- row (correct); cross-file collisions on the (product, platform, path)
-- triple now create separate rows with disambiguated slugs.

ALTER TABLE projects ADD COLUMN file_id TEXT;

-- Backfill: every existing project's flows share a file_id (UpsertFlow's
-- conflict key is (tenant, file_id, section, persona) — flows from the
-- same project can ONLY have come from the same Figma file). MIN() picks
-- the lexically-first file_id per project as a deterministic tiebreaker
-- in the rare case of historical drift; in practice every flow under a
-- given project has the same file_id today.
--
-- Welcome / system projects with no flows are left NULL — they're not
-- exported via the plugin, so the file_id concept doesn't apply.
UPDATE projects
   SET file_id = (
     SELECT MIN(f.file_id)
       FROM flows f
      WHERE f.project_id = projects.id
        AND f.deleted_at IS NULL
   )
 WHERE file_id IS NULL;

-- Partial unique on (tenant_id, file_id) — soft-deleted projects don't
-- block re-import of the same file. NULL file_ids are ignored by the
-- partial index, so the welcome project can stay around.
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_unique_file_id
    ON projects(tenant_id, file_id)
    WHERE file_id IS NOT NULL AND deleted_at IS NULL;
