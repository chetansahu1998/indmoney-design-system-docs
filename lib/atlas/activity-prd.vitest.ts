/**
 * activity-prd.vitest.ts — plan 005 U4. Covers the prd_audit-merge mappings
 * surfaced by auditLogToActivity for sub_flow-bound leaves. Backend writes
 * the `prd.<op>` event_types in HandleFlowActivity; the frontend adapter
 * has to render them as PM-friendly sentences with the "edit" kind so the
 * Activity tab styles them consistently.
 */

import { describe, expect, test } from "vitest";

import { auditLogToActivity } from "./data-adapters";

const NO_USERS = new Map<string, string>();

function row(eventType: string, overrides: Record<string, unknown> = {}) {
  return {
    id: "a1",
    ts: "2026-05-18T02:00:00Z",
    event_type: eventType,
    user_id: "u1",
    endpoint: "/v1/sub-flows/prd",
    status_code: 0,
    details: `{"sub_flow_id":"sf1","prd_state_id":"st1"}`,
    ...overrides,
  };
}

describe("auditLogToActivity — prd.* events (plan 005 U4)", () => {
  test("prd.upsert_state renders authored sentence + edit kind", () => {
    const out = auditLogToActivity(row("prd.upsert_state") as never, NO_USERS);
    expect(out.what).toBe("authored a PRD state");
    expect(out.kind).toBe("edit");
  });

  test("prd.add_event renders tracking-event sentence", () => {
    const out = auditLogToActivity(row("prd.add_event") as never, NO_USERS);
    expect(out.what).toBe("added a tracking event");
    expect(out.kind).toBe("edit");
  });

  test("prd.add_acceptance_criterion → acceptance-criterion sentence", () => {
    const out = auditLogToActivity(
      row("prd.add_acceptance_criterion") as never,
      NO_USERS,
    );
    expect(out.what).toBe("added an acceptance criterion");
  });

  test("prd.upsert_copy_string → copy-edit sentence", () => {
    const out = auditLogToActivity(
      row("prd.upsert_copy_string") as never,
      NO_USERS,
    );
    expect(out.what).toBe("edited PRD copy");
  });

  test("prd.attach_frame / prd.detach_frame name the frame action", () => {
    const attach = auditLogToActivity(row("prd.attach_frame") as never, NO_USERS);
    const detach = auditLogToActivity(row("prd.detach_frame") as never, NO_USERS);
    expect(attach.what).toBe("attached a Figma frame to a state");
    expect(detach.what).toBe("detached a Figma frame from a state");
  });

  test("unknown prd.* op falls back to a generic edit sentence, not raw token", () => {
    const out = auditLogToActivity(row("prd.future_op") as never, NO_USERS);
    expect(out.what).toBe("edited the PRD");
    expect(out.kind).toBe("edit");
  });

  test("non-prd event_types are unaffected by the new branch", () => {
    const drd = auditLogToActivity(row("drd.edit") as never, NO_USERS);
    expect(drd.what).toBe("edited DRD");
    expect(drd.kind).toBe("edit");
  });
});
