/**
 * comment-target.vitest.ts — plan 005 U5. Covers the DisplayComment
 * round-trip with the new target_kind / target_id fields, plus the
 * body-vs-text alias that lets historical fixtures keep rendering.
 */

import { describe, expect, test } from "vitest";

import { commentToDisplay } from "./data-adapters";

const NO_USERS = new Map<string, string>();

function row(overrides: Record<string, unknown> = {}) {
  return {
    id: "c1",
    target_kind: "prd_state",
    target_id: "state-abc",
    body: "cold state copy reads weird",
    author_user_id: "u1",
    author_email: "kavya@example.com",
    reaction_count: 0,
    created_at: "2026-05-18T02:00:00Z",
    ...overrides,
  };
}

describe("commentToDisplay — target_kind + target_id threading", () => {
  test("prd_state row carries targetKind + targetID into DisplayComment", () => {
    const out = commentToDisplay(row() as never, NO_USERS);
    expect(out.targetKind).toBe("prd_state");
    expect(out.targetID).toBe("state-abc");
    expect(out.body).toBe("cold state copy reads weird");
  });

  test("body field is preferred over legacy text alias", () => {
    const out = commentToDisplay(
      row({ body: "from-body", text: "from-text-fallback" }) as never,
      NO_USERS,
    );
    expect(out.body).toBe("from-body");
  });

  test("falls back to text when body is absent (historical fixtures)", () => {
    const out = commentToDisplay(
      row({ body: undefined, text: "legacy text" }) as never,
      NO_USERS,
    );
    expect(out.body).toBe("legacy text");
  });

  test("missing body AND text coerces to empty string, not undefined", () => {
    const out = commentToDisplay(
      row({ body: undefined, text: undefined }) as never,
      NO_USERS,
    );
    expect(out.body).toBe("");
  });

  test("drd_block row still produces a chip-suppressed display (kind kept on the type)", () => {
    // The UI suppresses the chip for kind=drd_block, but the field still
    // round-trips so future inspectors can opt in to surfacing it.
    const out = commentToDisplay(
      row({ target_kind: "drd_block", target_id: "block-1" }) as never,
      NO_USERS,
    );
    expect(out.targetKind).toBe("drd_block");
    expect(out.targetID).toBe("block-1");
  });
});
