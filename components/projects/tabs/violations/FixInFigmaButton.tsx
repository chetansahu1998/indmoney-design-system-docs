"use client";

/**
 * "Fix in Figma" deeplink button — visible only when the violation is
 * marked auto_fixable on the server side. Click opens the plugin with the
 * violation_id pre-loaded; the plugin (Phase 4 U11) fetches the violation
 * via GET /violations/:id, locates the offending node, and applies the
 * fix on user confirmation.
 *
 * The deeplink shape follows Figma's plugin-protocol convention. Phase 4
 * U11 wires the plugin-side handler; until that ships, the link still
 * works — clicking opens the plugin, which presents an "audit mode" tab.
 *
 * Plugin id is injected at build time via NEXT_PUBLIC_FIGMA_PLUGIN_ID
 * (defaulted to a placeholder so the link is visible during local dev
 * without the env var set).
 */

interface Props {
  violationID: string;
}

const PLUGIN_ID =
  process.env.NEXT_PUBLIC_FIGMA_PLUGIN_ID ?? "indmoney-design-system";

export default function FixInFigmaButton({ violationID }: Props) {
  const href = `figma://plugin/${encodeURIComponent(PLUGIN_ID)}/audit?violation_id=${encodeURIComponent(violationID)}`;
  return (
    <a
      href={href}
      title="Open this violation in the Figma plugin and apply the suggested fix"
      style={{
        padding: "6px 10px",
        fontSize: 11,
        fontFamily: "var(--font-mono)",
        background: "rgba(22,163,74,0.12)",
        color: "#16a34a",
        border: "1px solid #16a34a",
        borderRadius: 6,
        textDecoration: "none",
        cursor: "pointer",
        whiteSpace: "nowrap",
      }}
      data-testid="fix-in-figma"
    >
      Fix in Figma
    </a>
  );
}
