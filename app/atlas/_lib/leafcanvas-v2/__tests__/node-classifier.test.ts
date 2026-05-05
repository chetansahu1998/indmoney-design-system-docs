/**
 * node-classifier.test.ts — exercise classifyNode + parseVariantProps
 * against real Figma node names sampled from canonical_trees in the
 * production-ish DB (NRI VKYC + Plutus Term Deposit).
 *
 * No test runner is wired in this repo; throws on assertion failure
 * and exposes runAll() for shape-correct Vitest/Jest pickup.
 */

import { classifyNode, parseVariantProps, shouldRasterize } from "../node-classifier";
import type { CanonicalNode } from "../types";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

function node(
  type: string,
  name: string,
  extra: Partial<CanonicalNode> = {},
): CanonicalNode {
  return { type: type as CanonicalNode["type"], name, ...extra };
}

// ─── Icon taxonomy ────────────────────────────────────────────────────────

function test_icons_2d_help(): void {
  const c = classifyNode(node("INSTANCE", "Icons/ 2D/ Help"));
  assert(c.kind === "icon", `kind=${c.kind}`);
  assert(c.role === "icons/2d/help", `role=${c.role}`);
  assert(Array.isArray(c.taxonomy) && c.taxonomy.length === 3, "taxonomy length 3");
}

function test_icons_filled_trustmarker(): void {
  const c = classifyNode(node("INSTANCE", "Icons/ Filled Icons/ Trustmarker"));
  assert(c.kind === "icon", `kind=${c.kind}`);
  assert(c.taxonomy?.[0] === "Icons", "taxonomy[0]=Icons");
}

function test_lowercase_icon_alert(): void {
  const c = classifyNode(node("BOOLEAN_OPERATION", "icon/alert/error_24px"));
  assert(c.kind === "icon", `kind=${c.kind}`);
}

// ─── Yes/No slash variant ─────────────────────────────────────────────────

function test_help_no_24px(): void {
  const c = classifyNode(node("FRAME", "Help/No/24px"));
  assert(c.kind === "icon", `kind=${c.kind}`);
  assert(c.variantProps?.state === "No", `state=${c.variantProps?.state}`);
  assert(c.variantProps?.size === "24px", `size=${c.variantProps?.size}`);
}

function test_cross_yes_24px(): void {
  const c = classifyNode(node("FRAME", "Cross 1/Yes/24px"));
  assert(c.kind === "icon", `kind=${c.kind}`);
  assert(c.variantProps?.state === "Yes", `state=${c.variantProps?.state}`);
}

function test_shield_tick_no_20px(): void {
  const c = classifyNode(node("FRAME", "Shield-Tick/No/20px"));
  assert(c.kind === "icon", `kind=${c.kind}`);
  assert(c.variantProps?.size === "20px", `size=${c.variantProps?.size}`);
}

// ─── Illustrations ────────────────────────────────────────────────────────

function test_illustration_with_mode(): void {
  const c = classifyNode(
    node(
      "FRAME",
      "Illustrations/Equity tracking/Light/Banners/Got more demat accounts",
    ),
  );
  assert(c.kind === "illustration", `kind=${c.kind}`);
  assert(c.variantProps?.mode === "Light", `mode=${c.variantProps?.mode}`);
  assert(
    c.taxonomy?.length === 5,
    `taxonomy length=${c.taxonomy?.length} (want 5)`,
  );
}

function test_illustration_data_fetch_failed(): void {
  const c = classifyNode(
    node("FRAME", "Illustrations/Equity tracking/Light/Data fetch failed"),
  );
  assert(c.kind === "illustration", `kind=${c.kind}`);
  assert(c.variantProps?.mode === "Light", "mode=Light");
}

// ─── Layout-named INSTANCEs ───────────────────────────────────────────────

function test_status_bar_is_container(): void {
  const c = classifyNode(node("INSTANCE", "Status Bar"));
  assert(c.kind === "container", `kind=${c.kind} — Status Bar must NOT rasterize`);
}

function test_otp_input_is_container(): void {
  const c = classifyNode(node("INSTANCE", "OTP Input"));
  assert(c.kind === "container", `kind=${c.kind}`);
}

function test_rounded_rectangle_is_container(): void {
  // 89× occurrence in Plutus Term Deposit — these are styled background
  // shells, not icons. Must not rasterize.
  const c = classifyNode(node("INSTANCE", "Rounded Rectangle"));
  assert(c.kind === "container", `kind=${c.kind}`);
}

function test_toggle_final_is_container(): void {
  const c = classifyNode(node("INSTANCE", "Toggle Final"));
  assert(c.kind === "container", `kind=${c.kind}`);
}

// ─── Standalone shapes ────────────────────────────────────────────────────

function test_vector_is_shape(): void {
  const c = classifyNode(node("VECTOR", "Wifi-path"));
  assert(c.kind === "shape", `kind=${c.kind}`);
}

function test_ellipse_is_shape(): void {
  const c = classifyNode(node("ELLIPSE", "Ellipse 22023"));
  assert(c.kind === "shape", `kind=${c.kind}`);
}

// ─── parseVariantProps directly ───────────────────────────────────────────

function test_kv_variant_props(): void {
  // Figma's component-variant API syntax — Type=Primary, Size=Large.
  const props = parseVariantProps("Button/Type=Primary, Size=Large");
  assert(props?.type === "Primary", `type=${props?.type}`);
  assert(props?.size === "Large", `size=${props?.size}`);
}

function test_kv_with_dashes_in_key(): void {
  // Edge case: key contains a space — `Stroke Color=Default`.
  const props = parseVariantProps("Card / Stroke Color=Default, Layout=Tight");
  assert(props?.["stroke color"] === "Default", `got ${JSON.stringify(props)}`);
  assert(props?.layout === "Tight", `got ${JSON.stringify(props)}`);
}

function test_pure_size_no_state(): void {
  const props = parseVariantProps("error_24px");
  assert(props?.size === "24px", `size=${props?.size}`);
  assert(props?.state === undefined, "no state for pure-size segment");
}

// ─── shouldRasterize predicate (same source of truth used by hook + renderer) ──

function test_should_rasterize_icon_yes(): void {
  assert(shouldRasterize(node("INSTANCE", "Icons/ 2D/ Help")), "icon must rasterize");
}

function test_should_rasterize_layout_no(): void {
  assert(!shouldRasterize(node("INSTANCE", "Status Bar")), "Status Bar must NOT rasterize");
}

function test_should_rasterize_illustration_yes(): void {
  assert(
    shouldRasterize(
      node("FRAME", "Illustrations/Equity tracking/Light/Data fetch failed"),
    ),
    "illustration must rasterize",
  );
}

function test_should_rasterize_text_no(): void {
  assert(!shouldRasterize(node("TEXT", "Hello")), "TEXT must not rasterize");
}

// ─── Driver ───────────────────────────────────────────────────────────────

export function runAll(): void {
  const tests: Array<[string, () => void]> = [
    ["Icons/ 2D/ Help", test_icons_2d_help],
    ["Icons/ Filled Icons/ Trustmarker", test_icons_filled_trustmarker],
    ["icon/alert/error_24px (lowercase)", test_lowercase_icon_alert],
    ["Help/No/24px", test_help_no_24px],
    ["Cross 1/Yes/24px", test_cross_yes_24px],
    ["Shield-Tick/No/20px", test_shield_tick_no_20px],
    ["Illustration with mode=Light", test_illustration_with_mode],
    ["Illustration data fetch failed", test_illustration_data_fetch_failed],
    ["Status Bar is container", test_status_bar_is_container],
    ["OTP Input is container", test_otp_input_is_container],
    ["Rounded Rectangle is container", test_rounded_rectangle_is_container],
    ["Toggle Final is container", test_toggle_final_is_container],
    ["VECTOR Wifi-path is shape", test_vector_is_shape],
    ["ELLIPSE is shape", test_ellipse_is_shape],
    ["key=value variant props", test_kv_variant_props],
    ["multi-word key=value", test_kv_with_dashes_in_key],
    ["pure size no state", test_pure_size_no_state],
    ["shouldRasterize icon true", test_should_rasterize_icon_yes],
    ["shouldRasterize layout false", test_should_rasterize_layout_no],
    ["shouldRasterize illustration true", test_should_rasterize_illustration_yes],
    ["shouldRasterize text false", test_should_rasterize_text_no],
  ];
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      fn();
      // eslint-disable-next-line no-console
      console.log(`ok  ${name}`);
    } catch (err) {
      failed++;
      // eslint-disable-next-line no-console
      console.error(`fail ${name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  if (failed > 0) throw new Error(`${failed} node-classifier test(s) failed`);
}
