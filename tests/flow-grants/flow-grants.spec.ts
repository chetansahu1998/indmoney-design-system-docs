/**
 * Phase 7 — flow_grants ACL smoke specs.
 *
 * Per-flow access panel + grant/revoke require seeded fixtures (a flow
 * + a non-owner user). Backend invariants are covered by acl_test.go;
 * this Playwright file is a placeholder for the UI flow once seeds
 * land in the e2e fixture phase.
 */
import { test } from "@playwright/test";

test.describe("Flow grants UI", () => {
  test.skip("access-panel grant + revoke round-trip", () => {
    // Pending fixture seed: needs (tenant, flow, owner, grantee) before
    // the UI can be exercised end-to-end. Backend invariants are
    // validated by services/ds-service/internal/projects/acl_test.go.
  });
});
