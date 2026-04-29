package projects

import (
	"math"
	"sort"
)

// ModePairAdjacencyPx is the maximum |Δx| between two frames for them to be
// considered candidates for a mode pair. 10px matches the plugin's threshold
// (figma-plugin/code.ts) so the server's canonicalization agrees with what
// designers saw in the modal.
const ModePairAdjacencyPx = 10.0

// DetectModePairs groups frames into mode-pair sets. A mode pair is two-or-more
// frames that:
//
//   - share a Variable Collection ID,
//   - sit within ModePairAdjacencyPx of each other on the X axis,
//   - have different mode IDs (e.g. light + dark of the same screen),
//   - and pass a depth-2 structural-skeleton sanity check (Phase 1 stub —
//     Phase 2 audit will recompute against canonical_trees).
//
// Frames without a VariableCollectionID are returned as one-frame ModeGroups so
// the caller can still iterate over every frame uniformly.
//
// Output is stable: groups sorted by (collection_id, min_x), frames inside each
// group sorted by Y. This matters for tests and for downstream persist order.
func DetectModePairs(frames []FrameInfo) []ModeGroup {
	if len(frames) == 0 {
		return nil
	}

	// Bucket by collection_id first; frames without a collection_id end up in
	// the empty-string bucket and are emitted as singletons.
	byCollection := make(map[string][]FrameInfo)
	for _, f := range frames {
		byCollection[f.VariableCollectionID] = append(byCollection[f.VariableCollectionID], f)
	}

	var groups []ModeGroup

	// Singletons: frames with no collection ID never pair.
	if loose, ok := byCollection[""]; ok {
		for _, f := range loose {
			groups = append(groups, ModeGroup{
				VariableCollectionID: "",
				Frames:               []FrameInfo{f},
			})
		}
		delete(byCollection, "")
	}

	// Sorted iteration order so the output is deterministic.
	collectionKeys := make([]string, 0, len(byCollection))
	for k := range byCollection {
		collectionKeys = append(collectionKeys, k)
	}
	sort.Strings(collectionKeys)

	for _, cid := range collectionKeys {
		bucket := byCollection[cid]
		// Cluster frames within this collection by x-column adjacency.
		clusters := clusterByX(bucket, ModePairAdjacencyPx)
		for _, cluster := range clusters {
			// Within a cluster, frames with the SAME mode_id can't be a pair —
			// the same mode at the same column is genuinely the same screen
			// duplicated. Distinct-mode subsets become one ModeGroup.
			byMode := make(map[string][]FrameInfo)
			for _, f := range cluster {
				byMode[f.ModeID] = append(byMode[f.ModeID], f)
			}
			if len(byMode) < 2 {
				// All frames share a mode_id — emit one singleton per frame.
				for _, f := range cluster {
					groups = append(groups, ModeGroup{
						VariableCollectionID: cid,
						Frames:               []FrameInfo{f},
					})
				}
				continue
			}
			// Mode pair / triple: pick one frame per mode_id (the first one
			// after sorting by Y so the result is stable).
			sort.SliceStable(cluster, func(i, j int) bool { return cluster[i].Y < cluster[j].Y })
			seen := make(map[string]bool)
			var paired []FrameInfo
			for _, f := range cluster {
				if seen[f.ModeID] {
					// Extra duplicate at same column with same mode — emit as
					// its own singleton so it isn't silently dropped.
					groups = append(groups, ModeGroup{
						VariableCollectionID: cid,
						Frames:               []FrameInfo{f},
					})
					continue
				}
				seen[f.ModeID] = true
				paired = append(paired, f)
			}
			groups = append(groups, ModeGroup{
				VariableCollectionID: cid,
				Frames:               paired,
			})
		}
	}

	// Stable order: by collection_id, then by min(Y) within group, then by
	// frame_id as the tiebreaker.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].VariableCollectionID != groups[j].VariableCollectionID {
			return groups[i].VariableCollectionID < groups[j].VariableCollectionID
		}
		mi := minX(groups[i].Frames)
		mj := minX(groups[j].Frames)
		if mi != mj {
			return mi < mj
		}
		return groups[i].Frames[0].FrameID < groups[j].Frames[0].FrameID
	})

	return groups
}

// clusterByX greedily groups frames into x-column clusters. Two frames belong
// to the same cluster iff |Δx| < threshold to ANY frame already in the cluster
// (transitive). Returned as a slice of slices in the original input order.
func clusterByX(frames []FrameInfo, threshold float64) [][]FrameInfo {
	if len(frames) == 0 {
		return nil
	}
	// Sort by x so we can sweep linearly.
	sorted := make([]FrameInfo, len(frames))
	copy(sorted, frames)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].X < sorted[j].X })

	var clusters [][]FrameInfo
	current := []FrameInfo{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		// "Transitive" join: compare against the rightmost frame in the
		// current cluster (the one with the largest x). Sort guarantees that's
		// the cluster's last element. Threshold is exclusive (<10px) to match
		// the plugin's behavior.
		last := current[len(current)-1]
		if math.Abs(sorted[i].X-last.X) < threshold {
			current = append(current, sorted[i])
		} else {
			clusters = append(clusters, current)
			current = []FrameInfo{sorted[i]}
		}
	}
	clusters = append(clusters, current)
	return clusters
}

func minX(frames []FrameInfo) float64 {
	if len(frames) == 0 {
		return 0
	}
	m := frames[0].X
	for _, f := range frames[1:] {
		if f.X < m {
			m = f.X
		}
	}
	return m
}
