-- 0024_detected_organism_match — organism-pattern detection corpus (2026-05-13).
--
-- Phase 1 of the organism-pattern-detection plan. Pipeline Stage 6.7 (added
-- in services/ds-service/internal/projects/pipeline.go) walks every screen's
-- canonical_tree, fingerprints organism-shaped FRAMEs, classifies against
-- the published manifest's organism signatures, and writes one verdict row
-- per candidate frame here.
--
-- Why a new table and not the dormant `composition_refs` in types.go:184:
-- that struct was scaffolded for cross-VERSION composition refs (one project
-- version composes assets from another). Organism detection is a different
-- relationship — frame X structurally resembles published organism Y. The
-- two surfaces don't overlap; keeping them distinct avoids future readers
-- conflating them. See docs/plans/2026-05-13-001-feat-organism-pattern-
-- detection-plan.md → "Context & Research → Institutional Learnings".

CREATE TABLE IF NOT EXISTS detected_organism_match (
    -- (version_id, frame_id) is the primary identity. Re-running detection
    -- on the same version is an UPSERT; version-bumps create new rows so
    -- trend dashboards can chart adoption over time.
    version_id              TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    frame_id                TEXT NOT NULL,
    -- Owning screen + tenant for cascade safety and tenant-scoped reads.
    screen_id               TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- Best-guess slug from the published manifest. NULL when match_kind='novel'
    -- (no published organism matched).
    suspected_slug          TEXT,
    -- Variant identifier within the slug (e.g. "li=yes,ri=yes,rt=yes" for
    -- List on Surface). NULL when the suspected slug has no variant axis.
    suspected_variant_key   TEXT,
    -- 'exact'  — Jaccard(atom_set) = 1.0 AND slot_topology hash match
    -- 'near'   — Jaccard ≥ 0.5 OR atom_set match with slot drift
    -- 'novel'  — Jaccard < 0.5 (candidate for new-component promotion)
    match_kind              TEXT NOT NULL CHECK (match_kind IN ('exact','near','novel')),
    -- sha256_16 hex of (atom_set, slot_topology). Used to cluster identical
    -- novel patterns across frames + versions for Part D's promotion_candidate
    -- aggregation.
    fingerprint_hash        TEXT NOT NULL,
    -- Sorted unique atom slugs the frame's INSTANCE children resolve to,
    -- serialized as a JSON array string. Drives Jaccard re-computation when
    -- thresholds tune without a re-import.
    atom_signature_json     TEXT NOT NULL,
    -- Bbox-ordered slot list with inferred slot kinds (LEFT_ICON, RIGHT_TEXT,
    -- OVERLINE, etc.), serialized JSON.
    slot_topology_json      TEXT NOT NULL,
    -- Per-slot delta vs the best-match variant when match_kind='near'.
    -- Empty/NULL for 'exact' and 'novel'.
    diff_json               TEXT,
    -- 0.0–1.0 — Jaccard or weighted Jaccard+topology depending on classifier.
    confidence              REAL NOT NULL CHECK (confidence >= 0.0 AND confidence <= 1.0),
    -- sha256 of the manifest contents at detection time. Lets the dashboard
    -- surface stale-verdict warnings when the current manifest hash drifts
    -- from this row's stored hash.
    manifest_hash           TEXT NOT NULL,
    -- When this frame is nested inside another organism-shaped frame, the
    -- outer frame's figma node id. NULL for top-level matches.
    parent_frame_id         TEXT,
    -- ISO-8601 UTC.
    detected_at             TEXT NOT NULL,
    PRIMARY KEY (version_id, frame_id)
) STRICT;

-- Dashboard count queries: COUNT(*) BY (tenant, match_kind).
CREATE INDEX IF NOT EXISTS idx_detected_organism_tenant_kind
    ON detected_organism_match (tenant_id, match_kind);

-- Promotion-candidate clustering (Part D): join on fingerprint_hash for the
-- 'novel' subset specifically. Partial index keeps the index lean — the
-- 'novel' bucket is the smallest by design.
CREATE INDEX IF NOT EXISTS idx_detected_organism_fingerprint_novel
    ON detected_organism_match (tenant_id, fingerprint_hash)
    WHERE match_kind = 'novel';

-- Per-slug adoption + drill-in queries (Part C, U10/U11/U12).
CREATE INDEX IF NOT EXISTS idx_detected_organism_slug
    ON detected_organism_match (tenant_id, suspected_slug, match_kind);

-- Per-screen lookup (e.g. plugin verdict surface — Part B U8).
CREATE INDEX IF NOT EXISTS idx_detected_organism_screen
    ON detected_organism_match (tenant_id, screen_id);

-- ─── Promotion candidates (Part D — pre-shipped column set) ──────────────────
--
-- One row per unique fingerprint_hash that recurs ≥ K times across ≥ N files
-- within a single tenant. Re-aggregated at the end of every Stage 6.7 run
-- (RebuildPromotionCandidates). `dismissed_at` is pre-included so Part D U14's
-- "dismiss" action lands without a second migration.

CREATE TABLE IF NOT EXISTS promotion_candidate (
    tenant_id              TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    fingerprint_hash       TEXT NOT NULL,
    -- Aggregate counts at last rebuild time. Re-computed every Stage 6.7.
    frequency              INTEGER NOT NULL CHECK (frequency >= 0),
    file_count             INTEGER NOT NULL CHECK (file_count >= 0),
    -- 1.0 - normalized variance of slot_topology hashes within the cluster.
    -- 1.0 = identical topology across all members; closer to 0 = more drift.
    stability_score        REAL NOT NULL CHECK (stability_score >= 0.0 AND stability_score <= 1.0),
    -- Avg(atom_INSTANCE_count / total_descendant_count) across cluster members.
    -- Higher = members are predominantly atom-INSTANCE composition (good
    -- promotion candidate); lower = members include lots of raw shape leaves
    -- (worse candidate).
    atom_reuse_rate        REAL NOT NULL CHECK (atom_reuse_rate >= 0.0 AND atom_reuse_rate <= 1.0),
    -- Reviewer-set proposed component name. NULL until someone names it.
    proposed_name          TEXT,
    -- Set when a reviewer dismisses the candidate from the promotion panel.
    -- The row is preserved (for trend tracking) but suppressed from active
    -- views.
    dismissed_at           TEXT,
    -- ISO-8601 UTC.
    first_seen             TEXT NOT NULL,
    last_seen              TEXT NOT NULL,
    PRIMARY KEY (tenant_id, fingerprint_hash)
) STRICT;

-- Dashboard ranking: ORDER BY (frequency * stability_score * atom_reuse_rate) DESC,
-- LIMIT N. The composite isn't a stored expression (STRICT mode); the scoring
-- happens at query time. Index covers tenant_id so the filter is cheap; the
-- ranking computation reads the row's three scalar columns.
CREATE INDEX IF NOT EXISTS idx_promotion_candidate_tenant
    ON promotion_candidate (tenant_id, dismissed_at);
