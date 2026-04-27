/**
 * TypeScript mirror of services/ds-service/internal/audit/types.go.
 *
 * SchemaVersion is the contract between the Go audit core and the docs
 * site / plugin. Bumping the version on the Go side without updating
 * SCHEMA_VERSION here is a release blocker.
 *
 * Readers tolerate unknown fields (forward-compat); a major-version
 * mismatch logs a warning and falls back to a degraded display.
 */

export const SCHEMA_VERSION = "1.0";

export type Decision = "accept" | "reject" | "ambiguous";
export type Priority = "P1" | "P2" | "P3";

export interface FixCandidate {
  node_id: string;
  node_name: string;
  property: "fill" | "stroke" | "text" | "padding" | "radius" | "spacing" | "component" | string;
  observed: string;
  token_path: string;
  token_alias?: string;
  variable_id?: string;
  distance: number;
  usage_count: number;
  priority: Priority;
  reason: "drift" | "deprecated" | "unbound" | "custom" | string;
  rationale?: string;
  replaced_by?: string;
}

export interface MatchEvidence {
  component_key: number;
  name_lex: number;
  style_id: number;
  color: number;
}

export interface ComponentMatch {
  node_id: string;
  node_name: string;
  component_key?: string;
  score: number;
  decision: Decision;
  evidence: MatchEvidence;
  matched_slug?: string;
}

export interface Coverage {
  bound: number;
  total: number;
}

export interface TokenCoverage {
  fills: Coverage;
  text: Coverage;
  spacing: Coverage;
  radius: Coverage;
}

export interface ComponentSummary {
  from_ds: number;
  ambiguous: number;
  custom: number;
}

export interface AuditScreen {
  node_id: string;
  name: string;
  slug: string;
  coverage: TokenCoverage;
  component_summary: ComponentSummary;
  fixes: FixCandidate[];
  component_matches: ComponentMatch[];
  node_count: number;
}

export interface AuditResult {
  schema_version: string;
  file_key: string;
  file_name: string;
  file_slug: string;
  brand: string;
  owner?: string;
  extracted_at: string;
  file_rev?: string;
  design_system_rev: string;
  overall_coverage: number;
  overall_from_ds: number;
  headline_drift_hex?: string;
  screens: AuditScreen[];
  $extensions?: Record<string, unknown>;
}

export interface IndexEntry {
  file_key: string;
  file_name: string;
  file_slug: string;
  brand: string;
  extracted_at: string;
  overall_coverage: number;
  overall_from_ds: number;
  screen_count: number;
  headline_drift_hex?: string;
}

export interface CrossFilePattern {
  canonical_hash: string;
  node_count: number;
  files: string[];
  suggested_name?: string;
}

export interface TokenUseSite {
  file_slug: string;
  screen_slug: string;
  node_id: string;
  node_name?: string;
}

export interface TokenUsage {
  token_path: string;
  usage_count: number;
  file_count: number;
  use_sites?: TokenUseSite[];
}

export interface ComponentUsage {
  slug: string;
  usage_count: number;
  file_count: number;
}

export interface AuditIndex {
  schema_version: string;
  generated_at: string;
  design_system_rev: string;
  files: IndexEntry[];
  token_usage: TokenUsage[];
  component_usage: ComponentUsage[];
  cross_file_patterns: CrossFilePattern[];
  $extensions?: Record<string, unknown>;
}
