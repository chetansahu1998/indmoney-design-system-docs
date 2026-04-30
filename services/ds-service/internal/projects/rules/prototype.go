package rules

import (
	"context"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// PrototypeFetcher fetches Figma prototype connections for a file. The Phase 2
// flow-graph rule uses this on cache miss; the production implementation that
// calls Figma REST is wired by the orchestrator's prod-wire unit.
//
// fileID is the Figma file_id; screenIDs maps Figma node_id → our screen_id so
// the fetcher can attribute returned destinations back to local screens. Nodes
// without a corresponding screen_id (e.g. close/back/external) come back with
// DestinationScreenID = nil but DestinationNodeID populated.
//
// Implementations should be safe to call concurrently; they typically issue a
// single REST round-trip per fileID.
type PrototypeFetcher interface {
	FetchLinks(ctx context.Context, fileID string, screenIDs map[string]string) ([]projects.PrototypeLink, error)
}

// noopFetcher is the default fetcher used when none is configured. It returns
// zero links + nil error so the runner falls into the sparse-prototype fallback
// (skip orphan/dead-end/cycle, run state-coverage only).
type noopFetcher struct{}

// FetchLinks implements PrototypeFetcher with a zero-link return.
func (noopFetcher) FetchLinks(ctx context.Context, fileID string, screenIDs map[string]string) ([]projects.PrototypeLink, error) {
	return nil, nil
}

// NoopPrototypeFetcher returns a fetcher that always reports zero links. Useful
// in dev/test before a real Figma client is wired.
func NoopPrototypeFetcher() PrototypeFetcher { return noopFetcher{} }

// TODO(U5-prod-wire): the production PrototypeFetcher implementation.
//
// It must:
//   - GET /v1/files/{fileID}?branch_data=true&geometry=paths&depth=3 via the
//     existing audit Figma client (same auth path as services/ds-service/cmd/icons).
//   - Walk the file's `prototypeStartNodeID` and per-frame `transitions[]`.
//   - Attribute each transition's `destination_id` to a local screen via the
//     screenIDs map; preserve destination_node_id verbatim when no screen
//     reference exists (close / back / external / scroll-to).
//   - Cap fetch latency with a per-call timeout (suggest 10s p95).
//   - Cache token + retry/backoff against transient REST 5xx.
//
// The fetcher does NOT persist; the runner calls TenantRepo.UpsertPrototypeLinks
// after a successful fetch so the next audit on the same version is a cache hit.
