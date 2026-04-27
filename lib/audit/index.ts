/**
 * Public entry point for the audit data layer.
 *
 * UI code imports from `@/lib/audit` and never reaches into the raw JSON
 * directly — that lets us evolve the schema (or move the JSON to a
 * different location) without ripping through every component.
 */

export type {
  AuditIndex,
  AuditResult,
  AuditScreen,
  ComponentMatch,
  ComponentSummary,
  ComponentUsage,
  Coverage,
  CrossFilePattern,
  Decision,
  FixCandidate,
  IndexEntry,
  MatchEvidence,
  Priority,
  TokenCoverage,
  TokenUsage,
  TokenUseSite,
} from "./types";

export { SCHEMA_VERSION } from "./types";

export {
  hasAuditData,
  generatedAt,
  isStale,
  tokenUsage,
  componentUsage,
  auditedFiles,
  crossFilePatterns,
  allTokenUsage,
  allComponentUsage,
  asFileAudit,
  provenanceLine,
} from "./manifest";
