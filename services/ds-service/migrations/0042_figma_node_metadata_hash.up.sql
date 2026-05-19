-- 0035_figma_node_metadata_hash — change-detection signal for frame renames.
--
-- Context: the inventory poller's depth=14 file-wide fetch (the original
-- mechanism that populated figma_section.content_hash from the section's
-- subtree) consistently fails on big files (Mutual Funds V2, INDstocks V4,
-- US Stocks V2, etc.) — Figma's API returns 400 "Request too large" past
-- ~1 GB of response. For those files content_hash stays NULL forever and
-- the autosync planner has no way to detect frame-level changes.
--
-- This migration adds a second hash that uses the per-section depth=1
-- path the poller's NodeMetadataExtractor already runs (one /v1/files/
-- <key>/nodes?ids=<section_id>&depth=1 call per section, batched 50). Each
-- call returns just the section's direct FRAME/INSTANCE/COMPONENT children
-- — small, never busts the 1 GB cap, and crucially: catches frame renames
-- because the children's `name` field is in the response.
--
-- Hash shape: SHA-256 over the sorted concatenation of each row's
-- (node_id, name, type, parent_id, abs_x, abs_y, width, height) tuple.
-- See NodeMetadataExtractor.computeSectionHash() in the Go code.
--
-- Both columns are nullable; an unpopulated value means "never extracted"
-- and the planner treats it the same as a brand-new section (full_export).
-- Once populated, planner compares figma_section.node_metadata_hash
-- against figma_auto_sync_state.node_metadata_hash; mismatch triggers
-- a full re-export.

ALTER TABLE figma_section ADD COLUMN node_metadata_hash TEXT;

ALTER TABLE figma_auto_sync_state ADD COLUMN node_metadata_hash TEXT;

-- Indexed for the planner's diff query — `WHERE figma_section.node_metadata_hash
-- IS NOT NULL AND figma_section.node_metadata_hash != figma_auto_sync_state.node_metadata_hash`
-- benefits from a covering scan. Partial: skip the NULL rows since they
-- already trigger full_export via the existing new-section branch.
CREATE INDEX IF NOT EXISTS idx_figma_section_node_metadata_hash
    ON figma_section (tenant_id, file_key, node_metadata_hash)
    WHERE node_metadata_hash IS NOT NULL;
