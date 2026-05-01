package projects

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Phase 4 U9 — DS-lead dashboard aggregations.
//
// /v1/atlas/admin/summary returns five rollups so a DS lead can
// prioritize the org-wide design-system trajectory:
//
//   1. by_product       — Active violation counts grouped by Product.
//   2. by_severity      — Same data, severity dimension.
//   3. trend            — Weekly buckets over the requested window.
//   4. top_violators    — Components with the most active violations.
//   5. recent_decisions — Stub for Phase 5; empty array v1.
//
// All five run in parallel; SQLite handles concurrent read-only
// queries cleanly even on a single connection (the pool wraps each
// query). The struct exposes only summary numbers — no per-tenant
// detail leaks because the dashboard is super_admin-gated upstream.

// DashboardSummary is the response payload of HandleDashboardSummary.
type DashboardSummary struct {
	WeeksWindow      int                   `json:"weeks_window"`
	ByProduct        []ProductCount        `json:"by_product"`
	BySeverity       map[string]int        `json:"by_severity"`
	Trend            []TrendBucket         `json:"trend"`
	TopViolators     []TopViolator         `json:"top_violators"`
	RecentDecisions  []DashboardDecision   `json:"recent_decisions"`
	TotalActive      int                   `json:"total_active"`
	GeneratedAt      string                `json:"generated_at"`
}

type ProductCount struct {
	Product string `json:"product"`
	Active  int    `json:"active"`
}

type TrendBucket struct {
	WeekStart string `json:"week_start"` // RFC3339, Monday 00:00 UTC
	Active    int    `json:"active"`
	Fixed     int    `json:"fixed"`
}

type TopViolator struct {
	RuleID         string `json:"rule_id"`
	Category       string `json:"category"`
	ActiveCount    int    `json:"active_count"`
	HighestSeverity string `json:"highest_severity"`
}

// DashboardDecision carries the minimum fields the Recent Decisions
// panel renders — id + title + created_at + status, plus the project
// slug + flow_id Phase 5.2 added so admins can deep-link from
// /atlas/admin → /projects/<slug>?decision=<id> without a follow-up
// fetch.
type DashboardDecision struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	Status    string `json:"status"`
	FlowID    string `json:"flow_id"`
	Slug      string `json:"slug"`
}

// BuildDashboardSummary runs the five aggregations against the entire
// projects database. No tenant-scoping — the endpoint is super_admin
// only. Pass `weeksWindow` 4 / 8 / 12 / 24; out-of-range falls back to 8.
func BuildDashboardSummary(ctx context.Context, db *sql.DB, weeksWindow int) (DashboardSummary, error) {
	switch weeksWindow {
	case 4, 8, 12, 24:
		// ok
	default:
		weeksWindow = 8
	}

	out := DashboardSummary{
		WeeksWindow:     weeksWindow,
		BySeverity:      map[string]int{},
		ByProduct:       []ProductCount{},
		Trend:           []TrendBucket{},
		TopViolators:    []TopViolator{},
		RecentDecisions: []DashboardDecision{},
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	wg.Add(5)
	go func() {
		defer wg.Done()
		rows, err := db.QueryContext(ctx,
			`SELECT p.product, COUNT(*) AS n
			   FROM violations v
			   JOIN project_versions pv ON pv.id = v.version_id
			   JOIN projects p ON p.id = pv.project_id
			  WHERE v.status = 'active' AND p.deleted_at IS NULL
			  GROUP BY p.product
			  ORDER BY n DESC
			  LIMIT 50`)
		if err != nil {
			record(fmt.Errorf("by_product: %w", err))
			return
		}
		defer rows.Close()
		var local []ProductCount
		for rows.Next() {
			var pc ProductCount
			if err := rows.Scan(&pc.Product, &pc.Active); err != nil {
				record(err)
				return
			}
			local = append(local, pc)
		}
		mu.Lock()
		out.ByProduct = local
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		rows, err := db.QueryContext(ctx,
			`SELECT severity, COUNT(*)
			   FROM violations
			  WHERE status = 'active'
			  GROUP BY severity`)
		if err != nil {
			record(fmt.Errorf("by_severity: %w", err))
			return
		}
		defer rows.Close()
		local := map[string]int{}
		total := 0
		for rows.Next() {
			var sev string
			var n int
			if err := rows.Scan(&sev, &n); err != nil {
				record(err)
				return
			}
			local[sev] = n
			total += n
		}
		mu.Lock()
		out.BySeverity = local
		out.TotalActive = total
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		// Weekly buckets: take the violation's created_at, truncate to
		// week-of-year, count active vs fixed. We don't try to do exact
		// Monday-aligned weeks in SQLite — strftime('%Y-%W', ...) is the
		// idiomatic ISO-ish weeknum, sufficient for an 8-bucket trend.
		windowAgo := time.Now().UTC().AddDate(0, 0, -7*weeksWindow).Format(time.RFC3339)
		rows, err := db.QueryContext(ctx,
			`SELECT strftime('%Y-%W', created_at) AS bucket,
			        SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) AS active,
			        SUM(CASE WHEN status = 'fixed'  THEN 1 ELSE 0 END) AS fixed
			   FROM violations
			  WHERE created_at >= ?
			  GROUP BY bucket
			  ORDER BY bucket ASC`, windowAgo)
		if err != nil {
			record(fmt.Errorf("trend: %w", err))
			return
		}
		defer rows.Close()
		var local []TrendBucket
		for rows.Next() {
			var bucket string
			var active, fixed int
			if err := rows.Scan(&bucket, &active, &fixed); err != nil {
				record(err)
				return
			}
			local = append(local, TrendBucket{
				WeekStart: bucket,
				Active:    active,
				Fixed:     fixed,
			})
		}
		mu.Lock()
		out.Trend = local
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		rows, err := db.QueryContext(ctx,
			`SELECT rule_id, category, COUNT(*) AS n,
			        MIN(CASE severity
			            WHEN 'critical' THEN 1
			            WHEN 'high'     THEN 2
			            WHEN 'medium'   THEN 3
			            WHEN 'low'      THEN 4
			            ELSE 5 END) AS sev_rank
			   FROM violations
			  WHERE status = 'active'
			  GROUP BY rule_id, category
			  ORDER BY n DESC
			  LIMIT 10`)
		if err != nil {
			record(fmt.Errorf("top_violators: %w", err))
			return
		}
		defer rows.Close()
		var local []TopViolator
		for rows.Next() {
			var tv TopViolator
			var sevRank int
			if err := rows.Scan(&tv.RuleID, &tv.Category, &tv.ActiveCount, &sevRank); err != nil {
				record(err)
				return
			}
			tv.HighestSeverity = severityRankToString(sevRank)
			local = append(local, tv)
		}
		mu.Lock()
		out.TopViolators = local
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		// Phase 5 U10 + 5.2 P1 — recent decisions cross-tenant + the
		// slug + flow_id needed for /atlas/admin to deep-link into
		// the project's Decisions tab. JOIN cost is small (decisions
		// → project_versions → projects); the limit caps it at 20.
		rows, err := db.QueryContext(ctx,
			`SELECT d.id, d.title, d.made_at, d.status, d.flow_id, p.slug
			   FROM decisions d
			   JOIN project_versions pv ON pv.id = d.version_id
			   JOIN projects p ON p.id = pv.project_id
			  WHERE d.deleted_at IS NULL
			    AND d.status IN ('proposed', 'accepted', 'superseded')
			  ORDER BY d.made_at DESC
			  LIMIT 20`)
		if err != nil {
			record(fmt.Errorf("recent_decisions: %w", err))
			return
		}
		defer rows.Close()
		var local []DashboardDecision
		for rows.Next() {
			var d DashboardDecision
			if err := rows.Scan(&d.ID, &d.Title, &d.CreatedAt, &d.Status, &d.FlowID, &d.Slug); err != nil {
				record(err)
				return
			}
			local = append(local, d)
		}
		mu.Lock()
		out.RecentDecisions = local
		mu.Unlock()
	}()

	wg.Wait()
	return out, firstErr
}
