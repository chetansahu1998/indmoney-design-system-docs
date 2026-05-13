/**
 * TypeScript shapes for /atlas/figma-inventory.
 *
 * Mirror the Go DTOs in:
 *   services/ds-service/internal/projects/server_figma_inventory_admin.go
 *   services/ds-service/internal/projects/repository_figma_inventory.go
 *
 * Keep these in sync manually — when a Go field is added there, add it
 * here too. (No code-gen until the surface is bigger.)
 */

// ─── Team seeds ──────────────────────────────────────────────────────────────

export interface FigmaTeamSeed {
  team_id: string;
  team_name: string;
  added_by_user_id?: string;
  added_at: string; // RFC3339
  enabled: boolean;
  last_crawl_at?: string;
  last_crawl_status?: "ok" | "forbidden" | "error" | "";
  last_crawl_error?: string;
}

export interface ListTeamsResponse {
  teams: FigmaTeamSeed[];
  count: number;
}

export interface AddTeamRequest {
  team_id: string;
  team_name: string;
}

// ─── Inventory tree ──────────────────────────────────────────────────────────

export type InventoryNodeKind = "team" | "project" | "file" | "page" | "section";

export interface InventoryTreeNode {
  kind: InventoryNodeKind;
  id: string;
  name: string;
  // File-level
  last_modified?: string;
  thumbnail_url?: string;
  // Section-level — bbox in canvas coords. NULL on team/project/file/page.
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  // Soft-delete timestamp when include_deleted=1 was passed.
  deleted_at?: string;
  // Recursive children (each node carries its descendants inline).
  children?: InventoryTreeNode[];
}

// ─── Runs ────────────────────────────────────────────────────────────────────

export interface InventoryRun {
  id: number;
  started_at: string;
  finished_at?: string;
  duration_ms?: number;
  teams_crawled: number;
  projects_seen: number;
  files_seen: number;
  files_refetched: number;
  pages_upserted: number;
  sections_upserted: number;
  error_count: number;
  error_sample?: string[];
}

export interface ListRunsResponse {
  runs: InventoryRun[];
  count: number;
}

// ─── Sync trigger ────────────────────────────────────────────────────────────

export interface SyncTriggerResponse {
  triggered: boolean;
  at: string;
}
