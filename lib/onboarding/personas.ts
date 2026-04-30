/**
 * lib/onboarding/personas.ts — Phase 3 U10 — per-persona day-1 content.
 *
 * The /onboarding route renders one section per persona. Each section
 * embeds a screen-recording GIF (deferred polish — gifs not committed in
 * the U10 ship; the section falls back gracefully to title + description
 * + step list when the gif file is missing).
 *
 * Personas mirror the brainstorm's actor list (A1-A5). DS-lead and Admin
 * sections preview Phase 7 surfaces.
 *
 * Adding a persona: append a PersonaSpec entry. The route auto-renders
 * sections in array order; deeplinks /onboarding/<slug> resolve via the
 * `slug` field.
 */

export interface PersonaStep {
  /** Short title rendered as a numbered card heading. */
  title: string;
  /** 1-2 sentences explaining the action + outcome. */
  body: string;
  /** Optional inline link to a product surface (e.g. /projects). */
  cta?: { href: string; label: string };
}

export interface PersonaSpec {
  /** URL slug for the deeplink (e.g. /onboarding/designer). */
  slug: string;
  /** Display name. */
  name: string;
  /** One-line summary on the index page's persona-picker. */
  blurb: string;
  /** Optional gif filename under public/onboarding/ (e.g.
   *  "designer-export.gif"). When the file is missing the section
   *  renders without media — graceful degradation. */
  gif?: string;
  steps: PersonaStep[];
}

export const PERSONAS: readonly PersonaSpec[] = [
  {
    slug: "designer",
    name: "Designer (in-product team)",
    blurb:
      "You design + ship flows for your product. Today: export, review, iterate.",
    gif: "designer-export.gif",
    steps: [
      {
        title: "Install the Figma plugin",
        body:
          "From the Figma plugin marketplace, install Projects · Flow Atlas. The plugin adds a 4th mode (Projects) alongside Publish / Audit / Library.",
      },
      {
        title: "Select frames + run Projects mode",
        body:
          "Open the file you want to ship. Select N frames or sections. Open the plugin → Projects. The plugin auto-groups by enclosing SECTION + auto-detects light/dark mode pairs.",
      },
      {
        title: "Pick product, path, persona",
        body:
          "In the export modal, set the product (Plutus / Tax / Indian Stocks / etc.), the path within the product, and the persona (Default / KYC-Pending / Logged-out / etc.). Hit Send.",
      },
      {
        title: "Open the deeplink",
        body:
          "The plugin returns a deeplink to /projects/<slug>. Open it. The atlas renders frames at preserved (x, y); audit findings stream in over the next ~60s as workers complete each rule.",
        cta: { href: "/projects", label: "Browse your projects →" },
      },
      {
        title: "Address violations",
        body:
          "Switch to the Violations tab. Theme-parity Critical findings (hand-painted dark fills) and a11y contrast High findings are the highest-leverage to fix. View in JSON to see the offending node.",
      },
    ],
  },
  {
    slug: "pm",
    name: "Product Manager",
    blurb:
      "You review flows + comment on decisions. You don't author the DRD; you read it.",
    gif: "pm-review.gif",
    steps: [
      {
        title: "Bookmark /projects",
        body:
          "Your team's flows live in /projects grouped by product. Bookmark it; open at the start of every design review.",
        cta: { href: "/projects", label: "Open projects →" },
      },
      {
        title: "Toggle theme + persona to see edge cases",
        body:
          "Every project view has Theme + Persona toggles in the toolbar. Designers ship Default + light by default; toggle to dark + KYC-pending to spot edge-case regressions.",
      },
      {
        title: "Read the DRD before commenting",
        body:
          "The DRD tab is the design rationale doc. Decisions and links to Figma live here. Read it before commenting — the answer to your question is often already on the page.",
      },
      {
        title: "View in JSON when implementation is uncertain",
        body:
          "When a flow's behavior is ambiguous (e.g., what happens when the API errors?), open the JSON tab to see the canonical_tree. Variable bindings are resolved per the active mode.",
      },
    ],
  },
  {
    slug: "engineer",
    name: "Engineer",
    blurb:
      "You read flows at code-review time. You need fast access to the canonical_tree.",
    gif: "engineer-json.gif",
    steps: [
      {
        title: "Cmd-click a frame in the atlas",
        body:
          "On any project, click a frame to snap the atlas camera + switch the bottom pane to the JSON tab focused on that screen. Three-step flow becomes one.",
      },
      {
        title: "Read the canonical_tree",
        body:
          "The JSON tab renders the screen's canonical_tree (Figma node tree minus runtime noise). Bound variables show as chips with resolved hex swatches per the active mode.",
      },
      {
        title: "File a violation against your component",
        body:
          "If your component triggers a Critical theme-parity break or an a11y contrast High, the Violations tab tells you which rule + which screen. Open the GitHub issue with the screenshot from /projects/<slug>/screens/<id>/png.",
      },
      {
        title: "Re-export when you ship a fix",
        body:
          "Designers re-export from Figma after they fix the design. Engineers re-export after they fix the component. Both create a new Version; old versions stay readable.",
      },
    ],
  },
  {
    slug: "ds-lead",
    name: "DS Lead",
    blurb:
      "You curate the rule catalog + persona library. You watch the audit fan-out.",
    gif: "ds-lead-fanout.gif",
    steps: [
      {
        title: "Trigger a fan-out when a token publishes",
        body:
          "When you publish a token catalog change (e.g. renaming colour.surface.bg), every active flow's latest version needs to re-audit. Use the cmd/admin CLI: admin fanout --trigger=tokens_published --reason=\"…\".",
      },
      {
        title: "Watch the dashboard (Phase 4)",
        body:
          "Phase 4 ships the DS-lead dashboard with leaderboards (violations by Product / severity / trend). Until then, /projects + /violations sliced by category gives the same view manually.",
      },
      {
        title: "Curate the rule catalog (Phase 7)",
        body:
          "Phase 7 ships the rule curation editor. Until then, edit rule severities by hand in the audit_rules table or via SQL migration. The 28 seeded rules in migration 0003 are the starting set.",
      },
      {
        title: "Approve pending personas",
        body:
          "When designers add new personas via the plugin's free-text field, they land in audit_rules-style pending status. Phase 7 ships the approval surface; today, run an UPDATE on personas SET status='approved' WHERE id=…",
      },
    ],
  },
  {
    slug: "admin",
    name: "Tenant Admin",
    blurb:
      "You provision tenants + watch the worker pool. You're typically also the DS lead.",
    gif: "admin-deploy.gif",
    steps: [
      {
        title: "Run the migrations",
        body:
          "First boot: services/ds-service/cmd/server starts → migrations 0001…0005 apply automatically. The Welcome demo project lands at /projects/welcome (system tenant).",
      },
      {
        title: "Set DS_AUDIT_WORKERS",
        body:
          "Default 6 workers handles the AE-7 47-flow fan-out under 5min. Scale via env var DS_AUDIT_WORKERS for larger orgs (clamped to [1, 32]; SQLite serializes writes anyway).",
      },
      {
        title: "Check the deploy runbook",
        body:
          "docs/runbooks/2026-05-NN-phase-3-deploy.md covers Basis CLI install (KTX2 transcoding for atlas textures). When the CLI is missing, the server logs a warning and serves PNG only.",
      },
      {
        title: "Provision your first user",
        body:
          "POST /v1/admin/bootstrap with a one-time BOOTSTRAP_TOKEN creates the first super_admin. After that, the admin invites users via the standard signup flow.",
      },
    ],
  },
] as const;

/** Resolve a persona by slug; returns undefined for unknown slugs. */
export function getPersonaBySlug(
  slug: string,
): PersonaSpec | undefined {
  return PERSONAS.find((p) => p.slug === slug);
}
