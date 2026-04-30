/**
 * Static fixtures for `tests/projects/canvas-render.spec.ts`.
 *
 * Lives outside the spec to keep the test file readable; lives in TS rather
 * than JSON so authors can `import` it where needed and the type checker
 * keeps drift between the fixture shape and `lib/projects/types` honest.
 *
 * NOTE: field-name casing matches Go's default JSON encoding (PascalCase
 * struct fields with no `json:"…"` tag emit PascalCase keys). This is what
 * `lib/projects/types.ts` declares — see the comment at the top of that file
 * for the rationale.
 */

import type {
  Persona,
  Project,
  ProjectVersion,
  Screen,
  Violation,
} from "@/lib/projects/types";

export const FIXTURE_TENANT = "tenant-alpha";
export const FIXTURE_OTHER_TENANT = "tenant-beta";

export const FIXTURE_PROJECT: Project = {
  ID: "proj-fixture-1",
  Slug: "plutus-onboarding",
  Name: "Plutus Onboarding",
  Platform: "web",
  Product: "Plutus",
  Path: "Onboarding",
  OwnerUserID: "user-1",
  TenantID: FIXTURE_TENANT,
  DeletedAt: null,
  CreatedAt: "2026-04-01T10:00:00Z",
  UpdatedAt: "2026-04-20T10:00:00Z",
};

export const FIXTURE_VERSIONS: ProjectVersion[] = [
  {
    ID: "ver-1",
    ProjectID: FIXTURE_PROJECT.ID,
    TenantID: FIXTURE_TENANT,
    VersionIndex: 1,
    Status: "view_ready",
    Error: "",
    CreatedByUserID: "user-1",
    CreatedAt: "2026-04-20T10:00:00Z",
  },
];

export const FIXTURE_SCREENS: Screen[] = [
  {
    ID: "scr-1",
    VersionID: "ver-1",
    FlowID: "flow-1",
    TenantID: FIXTURE_TENANT,
    X: 0,
    Y: 0,
    Width: 1440,
    Height: 900,
    ScreenLogicalID: "logical-1",
    PNGStorageKey: null,
    CreatedAt: "2026-04-20T10:00:00Z",
  },
  {
    ID: "scr-2",
    VersionID: "ver-1",
    FlowID: "flow-1",
    TenantID: FIXTURE_TENANT,
    X: 1500,
    Y: 0,
    Width: 1440,
    Height: 900,
    ScreenLogicalID: "logical-2",
    PNGStorageKey: null,
    CreatedAt: "2026-04-20T10:00:00Z",
  },
];

export const FIXTURE_PERSONAS: Persona[] = [
  {
    ID: "persona-1",
    TenantID: FIXTURE_TENANT,
    Name: "KYC-pending",
    Status: "approved",
    CreatedByUserID: "user-1",
    CreatedAt: "2026-04-01T10:00:00Z",
  },
  {
    ID: "persona-2",
    TenantID: FIXTURE_TENANT,
    Name: "Default",
    Status: "approved",
    CreatedByUserID: "user-1",
    CreatedAt: "2026-04-01T10:00:00Z",
  },
];

export const FIXTURE_VIOLATIONS: Violation[] = [
  {
    ID: "viol-1",
    VersionID: "ver-1",
    ScreenID: "scr-1",
    TenantID: FIXTURE_TENANT,
    RuleID: "color/raw-hex",
    Severity: "high",
    Category: "token_drift",
    Property: "fills[0].color",
    Observed: "#FF6633",
    Suggestion: "use surface.brand.500",
    PersonaID: null,
    ModeLabel: "light",
    Status: "active",
    AutoFixable: true,
    CreatedAt: "2026-04-20T10:00:00Z",
  },
];
