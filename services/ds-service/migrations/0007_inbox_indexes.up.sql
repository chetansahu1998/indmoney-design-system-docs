-- 0007_inbox_indexes — Phase 4 U4 — designer inbox query path.
--
-- /v1/inbox returns the requesting user's Active violations across every
-- flow they own or are editor on. Worst-case load (per the plan): ~50
-- active violations × ~50 designers × 6 flows ≈ 2350 rows org-wide,
-- ~50 per designer.
--
-- The query joins violations × screens × flows × projects with a tenant
-- + status filter; severity DESC + created_at DESC are the default sort.
-- This composite covers the leading-column predicates (tenant_id, status)
-- and lets SQLite stop reading once it has the top-N sorted rows.

CREATE INDEX IF NOT EXISTS idx_violations_inbox
    ON violations(tenant_id, status, severity, created_at DESC);

-- Phase 7 will add a more selective index keyed by per-flow ACL grants
-- once that data exists; the Phase 4 inbox query falls back to project-
-- ownership + tenant-role gating, which the leading (tenant_id, status)
-- prefix handles cleanly.
