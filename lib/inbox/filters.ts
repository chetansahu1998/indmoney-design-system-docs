"use client";

/**
 * URL-bound inbox filter state. Filters live in the page URL so a designer
 * can deep-link to "Tax + Critical theme parity" and reload with the same
 * view.
 *
 * Phase 4 U5 keeps this as a thin wrapper around URLSearchParams. Phase 8
 * search will swap in a more sophisticated state container; the
 * read/write contract here stays stable so consumers don't change.
 */

import type { InboxFilters } from "./client";

export function parseInboxFiltersFromSearchParams(
  params: URLSearchParams,
): InboxFilters {
  const f: InboxFilters = {};
  const ruleID = params.get("rule_id");
  if (ruleID) f.rule_id = ruleID;
  const category = params.get("category");
  if (category) f.category = category;
  const persona = params.get("persona_id");
  if (persona) f.persona_id = persona;
  const mode = params.get("mode");
  if (mode) f.mode = mode;
  const project = params.get("project_id");
  if (project) f.project_id = project;
  const dateFrom = params.get("date_from");
  if (dateFrom) f.date_from = dateFrom;
  const dateTo = params.get("date_to");
  if (dateTo) f.date_to = dateTo;
  const limit = params.get("limit");
  if (limit) {
    const n = Number(limit);
    if (Number.isFinite(n) && n > 0) f.limit = n;
  }
  const offset = params.get("offset");
  if (offset) {
    const n = Number(offset);
    if (Number.isFinite(n) && n >= 0) f.offset = n;
  }
  const severities = params.getAll("severity");
  if (severities.length > 0) {
    f.severity = severities.filter(Boolean);
  }
  return f;
}

export function inboxFiltersToSearchParams(f: InboxFilters): URLSearchParams {
  const params = new URLSearchParams();
  if (f.rule_id) params.set("rule_id", f.rule_id);
  if (f.category) params.set("category", f.category);
  if (f.persona_id) params.set("persona_id", f.persona_id);
  if (f.mode) params.set("mode", f.mode);
  if (f.project_id) params.set("project_id", f.project_id);
  if (f.date_from) params.set("date_from", f.date_from);
  if (f.date_to) params.set("date_to", f.date_to);
  if (typeof f.limit === "number") params.set("limit", String(f.limit));
  if (typeof f.offset === "number") params.set("offset", String(f.offset));
  if (f.severity) {
    for (const s of f.severity) params.append("severity", s);
  }
  return params;
}

/** Returns whether two filter objects represent the same query. */
export function inboxFiltersEqual(a: InboxFilters, b: InboxFilters): boolean {
  const ka = inboxFiltersToSearchParams(a).toString();
  const kb = inboxFiltersToSearchParams(b).toString();
  return ka === kb;
}
