-- 0003_seed_audit_rules — Phase 2 default rule catalog
--
-- Seeds the audit_rules table with the rule_ids the Phase 2 RuleRunner registry
-- emits, plus the synthesized rule_ids produced by the Phase 1 audit-core
-- adapter (runner.go:ruleIDFor — "<reason>.<property>"). Each row carries the
-- default severity from the Phase 2 plan; per-tenant overrides land in Phase 7.
--
-- Idempotency: INSERT OR IGNORE so re-running the migration on an already-seeded
-- DB is a no-op. Phase 7 admin edits SET enabled / default_severity in place
-- (UPDATE), so re-seeding wouldn't clobber them anyway, but OR IGNORE makes the
-- intent explicit.
--
-- target_node_types: CSV of Figma node types the rule applies to. NULL = all.

-- ─── Phase 2 new rules ───────────────────────────────────────────────────────

-- Theme parity (U2). One rule_id, structural-diff finding per offending node.
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('theme_parity_break',
     'Theme parity break',
     'Mode pair (light/dark) diverges outside boundVariables — designer hand-painted a property in one mode without binding to a Variable.',
     'theme_parity', 'critical', 1, NULL, NULL, datetime('now'));

-- Cross-persona consistency (U3).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('cross_persona_component_gap',
     'Component coverage gap across personas',
     'Component used in one persona of a flow but missing from a peer persona of the same flow path.',
     'cross_persona', 'high', 1, 'INSTANCE', NULL, datetime('now'));

-- WCAG AA accessibility (U4).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('a11y_contrast_aa',
     'WCAG AA contrast (4.5:1 / 3:1 large)',
     'Text foreground/background contrast below WCAG 2.1 AA threshold (4.5:1 normal text, 3:1 for ≥18pt or ≥14pt bold).',
     'a11y_contrast', 'high', 1, 'TEXT', NULL, datetime('now')),
    ('a11y_unverifiable',
     'A11y contrast unverifiable',
     'Text appears over an image or complex gradient where automated contrast computation cannot return a confident value. Manual review required.',
     'a11y_contrast', 'info', 1, 'TEXT', NULL, datetime('now')),
    ('a11y_touch_target_44pt',
     'Touch target below 44×44pt',
     'Interactive component (button / link / icon button / tab) with width or height below 44pt — fails WCAG 2.5.5 target size.',
     'a11y_touch_target', 'high', 1, 'INSTANCE', NULL, datetime('now'));

-- Flow-graph (U5).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('flow_graph_orphan',
     'Orphan screen',
     'Screen has no inbound prototype edges and is not the start node — unreachable from the flow entry.',
     'flow_graph', 'medium', 1, NULL, NULL, datetime('now')),
    ('flow_graph_dead_end',
     'Dead-end screen',
     'Screen has zero outbound prototype edges and is not a recognized terminus (Success / Confirmation / Done / Error).',
     'flow_graph', 'medium', 1, NULL, NULL, datetime('now')),
    ('flow_graph_cycle',
     'Cycle without exit',
     'Strongly-connected component of two or more screens with no exit edge — flow can loop indefinitely.',
     'flow_graph', 'high', 1, NULL, NULL, datetime('now')),
    ('flow_graph_missing_state_coverage',
     'Missing state coverage',
     'Flow has a Loading screen but no Empty or Error peer. Common state-coverage gap.',
     'flow_graph', 'low', 1, NULL, NULL, datetime('now')),
    ('flow_graph_skipped',
     'Flow-graph audit skipped',
     'Prototype connection density too low to compute orphan / dead-end / cycle reliably (<0.5 edges per screen). Missing-state-coverage still ran.',
     'flow_graph', 'info', 1, NULL, NULL, datetime('now'));

-- Component governance (U6).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('component_detached',
     'Detached instance',
     'Node visually mimics a known design-system component but has no Figma componentId reference. Likely a detached instance.',
     'component_governance', 'medium', 1, 'FRAME,GROUP,RECTANGLE', NULL, datetime('now')),
    ('component_override_sprawl',
     'Override sprawl on instance',
     'Component instance carries 8+ overrides across componentProperties / boundVariables / direct visual props. Consider a new variant.',
     'component_governance', 'low', 1, 'INSTANCE', NULL, datetime('now')),
    ('component_set_sprawl',
     'Component sprawl in flow',
     'Flow uses 80+ distinct component sets — information-architecture concern; high cognitive load for designers and engineers.',
     'component_governance', 'info', 1, NULL, NULL, datetime('now'));

-- ─── Phase 1 audit-core adapter rules ────────────────────────────────────────
-- These rule_ids are produced by services/ds-service/internal/projects/runner.go
-- ruleIDFor() as <reason>.<property>. We seed them so the Phase 7 curation editor
-- has named entries to toggle, and so the category mapping is visible to ops.

-- Token drift (color / fill / stroke / generic).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('drift.fill',
     'Fill color drift',
     'Fill color is close to but not bound to a design-system token. Severity per Phase 1 priority mapping.',
     'token_drift', 'medium', 1, NULL, NULL, datetime('now')),
    ('drift.stroke',
     'Stroke color drift',
     'Stroke color is close to but not bound to a design-system token.',
     'token_drift', 'medium', 1, NULL, NULL, datetime('now')),
    ('drift.text',
     'Text style drift',
     'Text style does not match an approved DS text style (Heading / Body / Caption / etc.).',
     'text_style_drift', 'medium', 1, 'TEXT', NULL, datetime('now')),
    ('drift.padding',
     'Padding drift (4-pt grid)',
     'Padding value does not match the 4-pt grid; suggests nearest grid value.',
     'spacing_drift', 'low', 1, NULL, NULL, datetime('now')),
    ('drift.gap',
     'Auto-layout gap drift',
     'Gap between auto-layout children does not match the 4-pt grid.',
     'spacing_drift', 'low', 1, NULL, NULL, datetime('now')),
    ('drift.spacing',
     'Generic spacing drift',
     'Spacing value off-grid (4-pt). Generic spacing-drift bucket for non-padding/gap properties.',
     'spacing_drift', 'low', 1, NULL, NULL, datetime('now')),
    ('drift.radius',
     'Border radius drift',
     'Border radius does not match the pill rule or the multiples-of-2 ladder.',
     'radius_drift', 'low', 1, NULL, NULL, datetime('now')),
    ('drift.component',
     'Component match drift',
     'Component instance scored ambiguously against the DS catalog — accept/reject decision deferred.',
     'component_match', 'low', 1, 'INSTANCE', NULL, datetime('now'));

-- Deprecated tokens (P1 Critical per Phase 1 mapping; severity lowered to High
-- here so the catalog default is the *typical* case — the worker's MapPriority-
-- ToSeverity still elevates to Critical when fc.Reason='deprecated' AND
-- fc.Priority='P1'. Phase 7 admin can pin Critical here if desired.)
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('deprecated.fill',
     'Deprecated fill token',
     'Fill bound to a token that has been marked deprecated. Replace per the deprecation chain.',
     'token_drift', 'high', 1, NULL, NULL, datetime('now')),
    ('deprecated.stroke',
     'Deprecated stroke token',
     'Stroke bound to a deprecated token.',
     'token_drift', 'high', 1, NULL, NULL, datetime('now')),
    ('deprecated.text',
     'Deprecated text style',
     'Text bound to a deprecated text style.',
     'text_style_drift', 'high', 1, 'TEXT', NULL, datetime('now'));

-- Theme break (Phase 1 used theme_break.* for ad-hoc structural finds; Phase 2
-- replaces this with the explicit theme_parity_break rule above. We keep the
-- legacy rule_ids enabled with category=theme_parity so historical violations
-- continue to filter correctly until they re-audit and produce theme_parity_break
-- rows instead.)
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('theme_break.fill',
     'Theme break — fill (legacy)',
     'Phase 1 legacy rule. Phase 2 replaces with theme_parity_break.',
     'theme_parity', 'critical', 1, NULL, NULL, datetime('now')),
    ('theme_break.stroke',
     'Theme break — stroke (legacy)',
     'Phase 1 legacy rule. Phase 2 replaces with theme_parity_break.',
     'theme_parity', 'critical', 1, NULL, NULL, datetime('now')),
    ('theme_break.padding',
     'Theme break — padding (legacy)',
     'Phase 1 legacy rule. Phase 2 replaces with theme_parity_break.',
     'theme_parity', 'critical', 1, NULL, NULL, datetime('now')),
    ('theme_break.text',
     'Theme break — text (legacy)',
     'Phase 1 legacy rule. Phase 2 replaces with theme_parity_break.',
     'theme_parity', 'critical', 1, NULL, NULL, datetime('now'));

-- Unbound (raw value where a Variable should exist).
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('unbound.fill',
     'Unbound fill',
     'Raw color fill where a Variable binding is expected — likely a hand-painted swatch that should reference a token.',
     'token_drift', 'medium', 1, NULL, NULL, datetime('now')),
    ('unbound.stroke',
     'Unbound stroke',
     'Raw color stroke where a Variable binding is expected.',
     'token_drift', 'medium', 1, NULL, NULL, datetime('now')),
    ('unbound.text',
     'Unbound text style',
     'Raw text style where a textStyleId binding is expected.',
     'text_style_drift', 'medium', 1, 'TEXT', NULL, datetime('now')),
    ('unbound.component',
     'Unbound component instance',
     'Component instance has no resolved mainComponent — likely a stale reference. Re-link or remove.',
     'component_match', 'medium', 1, 'INSTANCE', NULL, datetime('now'));

-- Custom — non-DS shape that visually resembles a DS atom.
INSERT OR IGNORE INTO audit_rules
    (rule_id, name, description, category, default_severity, enabled, target_node_types, expression, created_at)
VALUES
    ('custom.component',
     'Custom shape resembling a DS component',
     'Group / frame / rectangle visually resembles a known design-system component. Convert to instance for parity and updates.',
     'component_governance', 'low', 1, 'FRAME,GROUP,RECTANGLE', NULL, datetime('now'));
