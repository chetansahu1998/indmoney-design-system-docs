// Command backfill-lod — one-shot generator for missing L1/L2 LOD sidecars.
//
// Background: the Figma render pipeline writes `<id>@2x.png` plus `.l1.png`
// (50%) and `.l2.png` (25%) sidecars (see internal/projects/pipeline.go).
// When a version was repaired by an out-of-band script that only emitted
// `@2x.png`, the LOD sidecars are absent and png_handler 404s on
// `?tier=l1|l2` requests.
//
// Usage (from repo root):
//
//	go run ./services/ds-service/cmd/backfill-lod \
//	    --root services/ds-service/data/screens \
//	    --tenant e090530f-2698-489d-934a-c821cb925c8a \
//	    --version 810061c4-9fe5-40b9-bfc8-f7f23ea1a123
//
// Without --tenant/--version it walks every subdirectory under --root.
// --dry-run reports what would be written without touching disk.
// Idempotent — skips files that already exist.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	root := flag.String("root", "services/ds-service/data/screens", "screens data root")
	tenant := flag.String("tenant", "", "tenant ID (optional — restricts to one)")
	version := flag.String("version", "", "version ID (optional — restricts to one, requires --tenant)")
	dryRun := flag.Bool("dry-run", false, "report only; do not write files")
	flag.Parse()

	if *version != "" && *tenant == "" {
		log.Fatal("--version requires --tenant")
	}

	scope := *root
	if *tenant != "" {
		scope = filepath.Join(*root, *tenant)
		if *version != "" {
			scope = filepath.Join(scope, *version)
		}
	}
	info, err := os.Stat(scope)
	if err != nil {
		log.Fatalf("stat %s: %v", scope, err)
	}
	if !info.IsDir() {
		log.Fatalf("%s is not a directory", scope)
	}

	tiers := []projects.LODTier{projects.LODL1, projects.LODL2}

	var scanned, wrote, skipped, failed int

	err = filepath.WalkDir(scope, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		// Match `<id>@2x.png` only — skip `.l1.png`, `.l2.png`, `.ktx2`,
		// and any non-PNG droppings.
		name := d.Name()
		if !strings.HasSuffix(name, "@2x.png") {
			return nil
		}
		scanned++

		input, rerr := os.ReadFile(path)
		if rerr != nil {
			log.Printf("read %s: %v", path, rerr)
			failed++
			return nil
		}

		base := strings.TrimSuffix(path, ".png")
		for _, t := range tiers {
			out := base + projects.LODSuffixFor(t) + ".png"
			if _, statErr := os.Stat(out); statErr == nil {
				skipped++
				continue
			}
			if *dryRun {
				fmt.Printf("DRY %s\n", out)
				wrote++
				continue
			}
			data, derr := projects.DownsampleByFraction(input, projects.LODFractionFor(t))
			if derr != nil {
				log.Printf("downsample %s @ %s: %v", path, t, derr)
				failed++
				continue
			}
			if werr := os.WriteFile(out, data, 0o644); werr != nil {
				log.Printf("write %s: %v", out, werr)
				failed++
				continue
			}
			wrote++
		}
		return nil
	})
	if err != nil {
		log.Fatalf("walk %s: %v", scope, err)
	}

	fmt.Printf("scope=%s scanned=%d wrote=%d skipped=%d failed=%d dry_run=%v\n",
		scope, scanned, wrote, skipped, failed, *dryRun)
}
