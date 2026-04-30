-- 0005_welcome_seed — Phase 3 U12 — synthetic "Welcome" demo project.
--
-- A fresh installer landing on `/projects` should NOT see an empty list.
-- This migration seeds one demo project visible to the system tenant so
-- the cold-start UX (Phase 3 U5 EmptyState welcome variant pointing at
-- /onboarding) is augmented by a working project the user can click.
--
-- All inserts are INSERT OR IGNORE so re-running on already-seeded DBs
-- is a no-op. Phase 7 admin work can later pin the welcome project's
-- visibility to "all tenants" via a special-cased ACL row; for now it's
-- visible only when the user authenticates as the system tenant
-- (typically dev/demo environments + the migrate-sidecars backfill
-- pipeline).
--
-- Fixed UUIDs are intentional — they're public, deterministic, and
-- safe to expose since the system tenant has no privileged access.

-- ─── System tenant + user (idempotent) ──────────────────────────────────────
-- Mirrors what cmd/migrate-sidecars/main.go's ensureSystemUserTenant
-- creates, but inline so this migration works on a clean install without
-- the CLI having run.
INSERT OR IGNORE INTO users (id, email, password_hash, role, created_at)
VALUES ('system', 'system@indmoney.local', 'x', 'user', '2026-05-01T00:00:00Z');

INSERT OR IGNORE INTO tenants (id, slug, name, status, plan_type, created_at, created_by)
VALUES ('system', 'system', 'System', 'active', 'free', '2026-05-01T00:00:00Z', 'system');

-- ─── Welcome project ────────────────────────────────────────────────────────
INSERT OR IGNORE INTO projects
    (id, slug, name, platform, product, path, owner_user_id, tenant_id,
     created_at, updated_at)
VALUES
    ('welcome-project',
     'welcome',
     'Welcome to Projects · Flow Atlas',
     'web',
     'DesignSystem',
     'docs/welcome',
     'system', 'system',
     '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z');

-- ─── Welcome flow ──────────────────────────────────────────────────────────
INSERT OR IGNORE INTO flows
    (id, project_id, tenant_id, file_id, section_id, name,
     created_at, updated_at)
VALUES
    ('welcome-flow',
     'welcome-project', 'system',
     'welcome-fixture', NULL,
     'Onboarding atoms tour',
     '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z');

-- ─── Version 1 ─────────────────────────────────────────────────────────────
INSERT OR IGNORE INTO project_versions
    (id, project_id, tenant_id, version_index, status,
     created_by_user_id, created_at)
VALUES
    ('welcome-v1',
     'welcome-project', 'system',
     1, 'view_ready',
     'system', '2026-05-01T00:00:00Z');

-- ─── 6 demo screens (one per atom: Button, Card, Toast, Modal, Alert,
-- IconButton). x stacks vertically; width/height match a typical web
-- breakpoint. screen_logical_id is stable across re-seeds. ─────────────────
INSERT OR IGNORE INTO screens
    (id, version_id, flow_id, tenant_id, x, y, width, height,
     screen_logical_id, created_at)
VALUES
    ('welcome-s1', 'welcome-v1', 'welcome-flow', 'system',
     0.0,    0.0, 1024.0, 800.0, 'welcome-button',     '2026-05-01T00:00:00Z'),
    ('welcome-s2', 'welcome-v1', 'welcome-flow', 'system',
     0.0,  900.0, 1024.0, 800.0, 'welcome-card',       '2026-05-01T00:00:00Z'),
    ('welcome-s3', 'welcome-v1', 'welcome-flow', 'system',
     0.0, 1800.0, 1024.0, 800.0, 'welcome-toast',      '2026-05-01T00:00:00Z'),
    ('welcome-s4', 'welcome-v1', 'welcome-flow', 'system',
     0.0, 2700.0, 1024.0, 800.0, 'welcome-modal',      '2026-05-01T00:00:00Z'),
    ('welcome-s5', 'welcome-v1', 'welcome-flow', 'system',
     0.0, 3600.0, 1024.0, 800.0, 'welcome-alert',      '2026-05-01T00:00:00Z'),
    ('welcome-s6', 'welcome-v1', 'welcome-flow', 'system',
     0.0, 4500.0, 1024.0, 800.0, 'welcome-iconbutton', '2026-05-01T00:00:00Z');

-- ─── Canonical trees (minimal but valid JSON the runner can parse) ─────────
-- Each tree exposes one INSTANCE node referencing a known DS component
-- slug so the JSON tab renders something meaningful. Real Figma exports
-- carry richer node trees; these are the simplest valid shape.
INSERT OR IGNORE INTO screen_canonical_trees (screen_id, canonical_tree, hash, updated_at)
VALUES
    ('welcome-s1',
     '{"id":"welcome-s1","name":"Button","type":"FRAME","children":[{"id":"welcome-s1.btn","name":"Button","type":"INSTANCE","mainComponent":{"componentSetKey":"Button","name":"Button"}}]}',
     'welcome-s1-hash', '2026-05-01T00:00:00Z'),
    ('welcome-s2',
     '{"id":"welcome-s2","name":"Card","type":"FRAME","children":[{"id":"welcome-s2.card","name":"Card","type":"INSTANCE","mainComponent":{"componentSetKey":"Card","name":"Card"}}]}',
     'welcome-s2-hash', '2026-05-01T00:00:00Z'),
    ('welcome-s3',
     '{"id":"welcome-s3","name":"Toast","type":"FRAME","children":[{"id":"welcome-s3.toast","name":"Toast","type":"INSTANCE","mainComponent":{"componentSetKey":"Toast","name":"Toast"}}]}',
     'welcome-s3-hash', '2026-05-01T00:00:00Z'),
    ('welcome-s4',
     '{"id":"welcome-s4","name":"Modal","type":"FRAME","children":[{"id":"welcome-s4.modal","name":"Modal","type":"INSTANCE","mainComponent":{"componentSetKey":"Modal","name":"Modal"}}]}',
     'welcome-s4-hash', '2026-05-01T00:00:00Z'),
    ('welcome-s5',
     '{"id":"welcome-s5","name":"Alert","type":"FRAME","children":[{"id":"welcome-s5.alert","name":"Alert","type":"INSTANCE","mainComponent":{"componentSetKey":"Alert","name":"Alert"}}]}',
     'welcome-s5-hash', '2026-05-01T00:00:00Z'),
    ('welcome-s6',
     '{"id":"welcome-s6","name":"IconButton","type":"FRAME","children":[{"id":"welcome-s6.icon","name":"IconButton","type":"INSTANCE","mainComponent":{"componentSetKey":"IconButton","name":"IconButton"}}]}',
     'welcome-s6-hash', '2026-05-01T00:00:00Z');

-- ─── Two demo violations (one per headline Phase 2 rule) ───────────────────
-- A theme-parity break on the Button screen + an a11y contrast finding on
-- the Toast screen. Severity matches the audit_rules catalog defaults.
INSERT OR IGNORE INTO violations
    (id, version_id, screen_id, tenant_id,
     rule_id, severity, category, property, observed, suggestion,
     status, auto_fixable, created_at)
VALUES
    ('welcome-viol-1',
     'welcome-v1', 'welcome-s1', 'system',
     'theme_parity_break', 'critical', 'theme_parity',
     'fill',
     'Button background is hand-painted in dark mode (raw color rgb(107,115,128))',
     'Bind Button background to the surface.button-cta token in both modes.',
     'active', 0, '2026-05-01T00:00:00Z'),
    ('welcome-viol-2',
     'welcome-v1', 'welcome-s3', 'system',
     'a11y_contrast_aa', 'high', 'a11y_contrast',
     'contrast',
     'Toast text contrast 3.20:1 fails WCAG 2.1 AA (4.5:1 minimum)',
     'Increase Toast text fill to a darker token to meet 4.5:1.',
     'active', 0, '2026-05-01T00:00:00Z');

-- ─── screen_modes (one default mode per screen so the JSON tab renders) ─────
INSERT OR IGNORE INTO screen_modes
    (id, screen_id, tenant_id, mode_label, figma_frame_id,
     explicit_variable_modes_json)
VALUES
    ('welcome-m1', 'welcome-s1', 'system', 'default', 'welcome-frame-1', '{}'),
    ('welcome-m2', 'welcome-s2', 'system', 'default', 'welcome-frame-2', '{}'),
    ('welcome-m3', 'welcome-s3', 'system', 'default', 'welcome-frame-3', '{}'),
    ('welcome-m4', 'welcome-s4', 'system', 'default', 'welcome-frame-4', '{}'),
    ('welcome-m5', 'welcome-s5', 'system', 'default', 'welcome-frame-5', '{}'),
    ('welcome-m6', 'welcome-s6', 'system', 'default', 'welcome-frame-6', '{}');

-- ─── flow_drd (placeholder content so the DRD tab isn't blank) ─────────────
INSERT OR IGNORE INTO flow_drd
    (flow_id, tenant_id, content_json, revision, schema_version,
     updated_at, updated_by_user_id)
VALUES
    ('welcome-flow', 'system',
     CAST('{"blocks":[{"type":"heading","content":[{"type":"text","text":"Welcome to Projects · Flow Atlas"}]},{"type":"paragraph","content":[{"type":"text","text":"This demo project shows how DRD lives alongside the atlas + violations + JSON tabs. Type / for a block menu (custom blocks like /decision ship in Phase 5)."}]}]}' AS BLOB),
     1, '1.0',
     '2026-05-01T00:00:00Z', 'system');
