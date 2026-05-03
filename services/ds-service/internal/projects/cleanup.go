package projects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DefaultVersionRetention is the number of most-recent view_ready / failed
// versions per project whose PNG directory the prune sweeper keeps.
// Anything older has its on-disk frames removed; the SQLite rows stay
// intact. Override at the cmd/cleanup-versions invocation site or via the
// VERSION_RETENTION env var.
//
// Plan 2026-05-03-001 / T6.
const DefaultVersionRetention = 3

// PruneOldVersionDirs removes the on-disk PNG cache for every version of
// `projectID` past the `retain` most-recent (ordered by version_index DESC).
// Idempotent: rows with pruned_at IS NOT NULL are skipped, so re-running
// the sweeper is cheap.
//
// The SQLite `screens` rows + `screen_canonical_trees` blobs stay in
// place — only the rendered PNGs are reclaimed. If a designer ever opens
// an older version's URL, the PNG handler 404s (frames un-rendered);
// re-render via cmd/server's HandleVersionRetry brings them back.
func PruneOldVersionDirs(ctx context.Context, log *slog.Logger, repo *TenantRepo, dataDir, projectID string, retain int) (pruned, kept int, err error) {
	if retain < 1 {
		retain = 1
	}
	versions, err := repo.ListVersionsByProject(ctx, projectID)
	if err != nil {
		return 0, 0, fmt.Errorf("list versions: %w", err)
	}
	if len(versions) <= retain {
		return 0, len(versions), nil
	}

	// versions are ordered by version_index DESC by ListVersionsByProject.
	// First `retain` are kept; the rest are candidates.
	candidates := versions[retain:]
	for _, v := range candidates {
		// Skip already-pruned, pending, or in-flight versions.
		if v.Status == "pending" {
			continue
		}
		if v.PrunedAt != nil {
			continue
		}
		dir := filepath.Join(dataDir, "screens", v.TenantID, v.ID)
		if rmErr := os.RemoveAll(dir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			// Don't abort the whole sweep on one bad directory — log
			// and continue. The next sweeper pass will retry, so a
			// transient EBUSY on a busy disk recovers on its own.
			if log != nil {
				log.Warn("cleanup: rm screens dir failed",
					"version_id", v.ID, "dir", dir, "err", rmErr.Error())
			}
			continue
		}
		now := time.Now().UTC()
		if mErr := repo.MarkVersionPruned(ctx, v.ID, now); mErr != nil {
			if log != nil {
				log.Warn("cleanup: mark pruned_at failed",
					"version_id", v.ID, "err", mErr.Error())
			}
			continue
		}
		pruned++
	}
	return pruned, retain, nil
}
