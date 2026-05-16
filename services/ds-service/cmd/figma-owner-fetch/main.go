// cmd/figma-owner-fetch — one-shot CLI that backfills figma_file.last_editor_*
// from /v1/files/<key>/versions for every file that doesn't yet have an owner
// recorded. Drives the autosync owner-filter.
//
// Usage:
//
//	go run ./cmd/figma-owner-fetch \
//	    -tenant e090530f-2698-489d-934a-c821cb925c8a \
//	    -since 2025-11-14T00:00:00Z
//
// Defaults: 6-month window from now; tier-3 rate limit applies (80/min);
// stops on first transport error (caller can re-run, idempotent).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	var (
		tenantID = flag.String("tenant", "", "tenant_id (required)")
		dbPath   = flag.String("db", "", "path to ds.db (default: discovered upward)")
		sinceStr = flag.String("since", "", "RFC3339 cutoff; default: now - 6 months")
		limit    = flag.Int("limit", 0, "cap files processed (0 = all)")
	)
	flag.Parse()

	if *tenantID == "" {
		fmt.Fprintln(os.Stderr, "usage: figma-owner-fetch -tenant <id> [-since <RFC3339>] [-limit N]")
		os.Exit(2)
	}

	loadDotEnv()
	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		fmt.Fprintln(os.Stderr, "FIGMA_PAT not set")
		os.Exit(1)
	}

	since := time.Now().AddDate(0, -6, 0)
	if *sinceStr != "" {
		t, err := time.Parse(time.RFC3339, *sinceStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad -since:", err)
			os.Exit(2)
		}
		since = t
	}

	dbFile := *dbPath
	if dbFile == "" {
		dbFile = findDB()
	}
	if dbFile == "" {
		fmt.Fprintln(os.Stderr, "cannot locate ds.db — pass -db <path>")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "opening db: %s\n", dbFile)

	d, err := db.Open(dbFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db open:", err)
		os.Exit(1)
	}
	defer d.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	_ = logger

	repo := projects.NewTenantRepo(d.DB, *tenantID)
	fc := client.New(pat)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	files, err := repo.ListFilesNeedingOwnerFetch(ctx, since, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list files:", err)
		os.Exit(1)
	}
	fmt.Printf("files needing owner-fetch: %d (cutoff=%s)\n", len(files), since.Format(time.RFC3339))

	var ok, missing, fail int
	for i, f := range files {
		resp, err := fc.GetFileVersions(ctx, f.FileKey)
		if err != nil {
			fail++
			fmt.Printf("  [%d/%d] %s — ERR %v\n", i+1, len(files), shortKey(f.FileKey), err)
			continue
		}
		if len(resp.Versions) == 0 {
			missing++
			fmt.Printf("  [%d/%d] %s — no versions returned\n", i+1, len(files), shortKey(f.FileKey))
			continue
		}
		v := resp.Versions[0]
		at, _ := time.Parse(time.RFC3339, v.CreatedAt)
		e := projects.FigmaFileLastEditor{
			UserID: v.User.ID,
			Handle: v.User.Handle,
			Name:   strings.TrimSpace(v.User.Name),
			At:     at,
		}
		// Some PATs only see handle; fall back so the allowlist join has something.
		if e.Name == "" && e.Handle != "" {
			e.Name = e.Handle
		}
		if err := repo.UpdateFigmaFileLastEditor(ctx, f.FileKey, e); err != nil {
			fail++
			fmt.Printf("  [%d/%d] %s — DB write failed: %v\n", i+1, len(files), shortKey(f.FileKey), err)
			continue
		}
		ok++
		if i%10 == 0 || i == len(files)-1 {
			fmt.Printf("  [%d/%d] %s — %s (%s)\n", i+1, len(files), shortKey(f.FileKey), e.Name, f.Name)
		}
	}

	fmt.Printf("\ndone — ok=%d missing=%d fail=%d\n", ok, missing, fail)
}

func shortKey(k string) string {
	if len(k) > 10 {
		return k[:10]
	}
	return k
}

func findDB() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		alt := filepath.Join(d, "data", "ds.db")
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
		candidate := filepath.Join(d, "services", "ds-service", "data", "ds.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func loadDotEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		path := filepath.Join(d, ".env.local")
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			v = strings.Trim(v, `"'`)
			if _, ok := os.LookupEnv(k); !ok {
				os.Setenv(k, v)
			}
		}
		return
	}
}
