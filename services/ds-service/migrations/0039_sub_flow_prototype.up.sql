-- 0039_sub_flow_prototype — KTD-8 prototype-as-placeholder lifecycle.
--
-- Plan: docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md
--   U3b — DRD prototype attachment + canvas lifecycle gate.
--
-- A PM can attach an HTML prototype URL to a sub_flow while the design is
-- still in flight. The viewer renders this URL in a sandboxed iframe in
-- the canvas slot until autosync detects the bound section sitting on a
-- "final"-classified Figma page (mig 0029 page_classification = 'final'),
-- at which point figma_section_id is linked and prototype_superseded_at
-- is stamped. The prototype URL stays on the row for history.
--
-- Why no new page-name column on sub_product: per Execution Notes §A.3,
-- we reuse the existing figma_page_classifier.PageClassFinal regex (mig
-- 0029, populated during syncFileDeep). The autosync "design-shipped"
-- gate consults figma_page.page_classification directly.
--
-- Columns:
--   prototype_url            — HTTPS URL the viewer renders in an iframe.
--                              v1 accepts any HTTPS host. Repo validates
--                              <= 2048 chars + https:// scheme.
--   prototype_title          — optional human label shown in the viewer
--                              (e.g. "Cold State v3 — Figma proto").
--   prototype_attached_at    — RFC3339 stamp set on first AttachPrototype.
--                              Preserved across re-attach (idempotent).
--   prototype_superseded_at  — RFC3339 stamp set the moment autosync
--                              links figma_section_id while a prototype
--                              URL is present. Signals to the viewer
--                              that the Figma frames now take precedence
--                              over the iframe.
--
-- All four columns are nullable + sparse — most sub_flows never get a
-- prototype attached. No new indexes: lookups are always by sub_flow.id,
-- already covered by the primary key.

ALTER TABLE sub_flow ADD COLUMN prototype_url TEXT;
ALTER TABLE sub_flow ADD COLUMN prototype_title TEXT;
ALTER TABLE sub_flow ADD COLUMN prototype_attached_at TEXT;
ALTER TABLE sub_flow ADD COLUMN prototype_superseded_at TEXT;
