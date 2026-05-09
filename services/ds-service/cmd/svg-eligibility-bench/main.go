// svg-eligibility-bench measures the share of clusters that would route
// to the SVG path on the next pipeline run. One-shot measurement against
// the live ds.db — does not call Figma, does not write anything.
//
// Usage: SQLITE_PATH=services/ds-service/data/ds.db go run ./cmd/svg-eligibility-bench

package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	flowID := flag.String("flow", "", "if set, scope to one flow_id; otherwise sample across all flows")
	limit := flag.Int("limit", 5000, "max screens to walk")
	flag.Parse()

	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("services", "ds-service", "data", "ds.db")
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var rows *sql.Rows
	if *flowID != "" {
		rows, err = conn.DB.QueryContext(ctx,
			`SELECT s.id, COALESCE(t.canonical_tree, ''), t.canonical_tree_gz, t.canonical_tree_zstd
			   FROM screens s
			   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
			  WHERE s.flow_id = ?
			  LIMIT ?`, *flowID, *limit)
	} else {
		rows, err = conn.DB.QueryContext(ctx,
			`SELECT s.id, COALESCE(t.canonical_tree, ''), t.canonical_tree_gz, t.canonical_tree_zstd
			   FROM screens s
			   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
			  LIMIT ?`, *limit)
	}
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var totalScreens, totalClusters, svgEligible int
	reasonHisto := map[string]int{}
	for rows.Next() {
		var screenID string
		var legacy string
		var gz, zstdBlob []byte
		if err := rows.Scan(&screenID, &legacy, &gz, &zstdBlob); err != nil {
			log.Fatalf("scan: %v", err)
		}
		treeJSON, derr := projects.ResolveCanonicalTree(legacy, gz, zstdBlob)
		if derr != nil || treeJSON == "" {
			continue
		}
		totalScreens++
		for _, c := range projects.ExtractClustersWithSVGFlag([]byte(treeJSON)) {
			totalClusters++
			if c.SVGEligible {
				svgEligible++
				continue
			}
			// Record the FIRST reason since the walker short-circuits.
			if len(c.SkipReasons) > 0 {
				reasonHisto[c.SkipReasons[0]]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("rows.Err: %v", err)
	}

	fmt.Printf("Screens walked:     %d\n", totalScreens)
	fmt.Printf("Total clusters:     %d\n", totalClusters)
	if totalClusters == 0 {
		return
	}
	fmt.Printf("SVG-eligible:       %d (%.1f%%)\n",
		svgEligible, float64(svgEligible)*100/float64(totalClusters))
	fmt.Printf("Raster fallback:    %d (%.1f%%)\n",
		totalClusters-svgEligible,
		float64(totalClusters-svgEligible)*100/float64(totalClusters))
	if len(reasonHisto) == 0 {
		return
	}
	fmt.Println("\nTop reasons clusters fell back to raster:")
	type kv struct {
		k string
		v int
	}
	keys := make([]kv, 0, len(reasonHisto))
	for k, v := range reasonHisto {
		keys = append(keys, kv{k, v})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].v > keys[j].v })
	for i, x := range keys {
		if i >= 8 {
			break
		}
		fmt.Printf("  %-40s %5d (%.1f%%)\n", x.k, x.v,
			float64(x.v)*100/float64(totalClusters-svgEligible))
	}
}
