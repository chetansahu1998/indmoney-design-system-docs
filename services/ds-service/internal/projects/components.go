package projects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Phase 4 U7 — per-component reverse view.
//
// "Where this breaks" surfaces every violation that involves a given
// component (by display name) across every flow the caller can see.
// Component-governance rules emit violations whose `observed` field
// is `"<component_name> at <path-hint>"` (see
// internal/projects/rules/component_governance.go), so substring
// matching on the component name is the cheapest way to get correct
// results without a schema change.
//
// Cross-tenant aggregate vs. tenant-scoped detail (per the plan):
//   - The aggregate counts (severity tally + flow count) span every
//     tenant — DS leads need org-wide signal to prioritize component
//     fixes that fan out.
//   - The per-flow detail rows respect the caller's tenant boundary
//     so designers in tenant B never see tenant A's flow names.

// ComponentViolationsAggregate is the cross-tenant rollup returned with
// every per-component reverse view. Numbers only — no per-tenant detail.
type ComponentViolationsAggregate struct {
	TotalViolations int            `json:"total_violations"`
	BySeverity      map[string]int `json:"by_severity"`
	BySetSprawl     int            `json:"by_set_sprawl"`
	BySetDetached   int            `json:"by_set_detached"`
	BySetOverride   int            `json:"by_set_override"`
	FlowCount       int            `json:"flow_count"`
}

// ComponentViolationFlowRow is one per-flow row in the reverse view.
// Tenant-scoped (callers see only their own tenant's flows here).
type ComponentViolationFlowRow struct {
	ProjectID       string `json:"project_id"`
	ProjectSlug     string `json:"project_slug"`
	ProjectName     string `json:"project_name"`
	Product         string `json:"product"`
	FlowID          string `json:"flow_id"`
	FlowName        string `json:"flow_name"`
	ViolationCount  int    `json:"violation_count"`
	HighestSeverity string `json:"highest_severity"`
}

// ComponentViolations runs the two queries. Tenant-scoping for the per-
// flow detail uses the same role gate as the inbox (Phase 4 visibility
// model — Phase 7 ACL grants extend without changing the call shape).
//
// The leading wildcard on the LIKE pattern matches `observed` strings
// shaped like "Toast/Default at <path>" or "Toast at <path>". The cost
// is bounded by the component-governance rule_id filter — even with
// 50k violations org-wide we scan ~5% of them per query.
func ComponentViolations(ctx context.Context, db *sql.DB, callerTenantID string, isEditor bool, callerUserID, componentName string) (ComponentViolationsAggregate, []ComponentViolationFlowRow, error) {
	if strings.TrimSpace(componentName) == "" {
		return ComponentViolationsAggregate{}, nil, fmt.Errorf("component name required")
	}
	pattern := componentName + "%"

	// ─── Cross-tenant aggregate ────────────────────────────────────────────
	agg := ComponentViolationsAggregate{BySeverity: map[string]int{}}

	rows, err := db.QueryContext(ctx,
		`SELECT v.severity, v.rule_id, COUNT(*)
		   FROM violations v
		  WHERE v.status = 'active'
		    AND v.rule_id IN ('component_detached', 'component_override_sprawl', 'component_set_sprawl')
		    AND v.observed LIKE ?
		  GROUP BY v.severity, v.rule_id`,
		pattern,
	)
	if err != nil {
		return ComponentViolationsAggregate{}, nil, fmt.Errorf("aggregate query: %w", err)
	}
	for rows.Next() {
		var sev, rule string
		var n int
		if err := rows.Scan(&sev, &rule, &n); err != nil {
			rows.Close()
			return ComponentViolationsAggregate{}, nil, err
		}
		agg.BySeverity[sev] += n
		agg.TotalViolations += n
		switch rule {
		case "component_detached":
			agg.BySetDetached += n
		case "component_override_sprawl":
			agg.BySetOverride += n
		case "component_set_sprawl":
			agg.BySetSprawl += n
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ComponentViolationsAggregate{}, nil, err
	}
	rows.Close()

	// Distinct flow count cross-tenant (signal to the component owner
	// of "fan-out" — how many places does this break across the org).
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT s.flow_id)
		   FROM violations v
		   JOIN screens s ON s.id = v.screen_id
		  WHERE v.status = 'active'
		    AND v.rule_id IN ('component_detached', 'component_override_sprawl', 'component_set_sprawl')
		    AND v.observed LIKE ?`,
		pattern,
	).Scan(&agg.FlowCount); err != nil {
		return ComponentViolationsAggregate{}, nil, fmt.Errorf("flow count: %w", err)
	}

	// ─── Per-flow detail (tenant-scoped) ──────────────────────────────────
	clauses := []string{
		"v.status = 'active'",
		"v.rule_id IN ('component_detached', 'component_override_sprawl', 'component_set_sprawl')",
		"v.observed LIKE ?",
		"v.tenant_id = ?",
		"p.deleted_at IS NULL",
	}
	args := []any{pattern, callerTenantID}
	if !isEditor {
		clauses = append(clauses, "p.owner_user_id = ?")
		args = append(args, callerUserID)
	}
	whereSQL := strings.Join(clauses, " AND ")

	q := `SELECT p.id, p.slug, p.name, p.product,
	             f.id, f.name,
	             COUNT(*) AS violation_count,
	             MIN(CASE v.severity
	                   WHEN 'critical' THEN 1
	                   WHEN 'high'     THEN 2
	                   WHEN 'medium'   THEN 3
	                   WHEN 'low'      THEN 4
	                   ELSE 5
	                 END) AS highest_sev_rank
	        FROM violations v
	        JOIN screens s ON s.id = v.screen_id
	        JOIN flows f ON f.id = s.flow_id
	        JOIN project_versions pv ON pv.id = v.version_id
	        JOIN projects p ON p.id = pv.project_id
	       WHERE ` + whereSQL + `
	       GROUP BY p.id, f.id
	       ORDER BY violation_count DESC
	       LIMIT 200`

	frows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return ComponentViolationsAggregate{}, nil, fmt.Errorf("flow detail: %w", err)
	}
	defer frows.Close()

	out := []ComponentViolationFlowRow{}
	for frows.Next() {
		var r ComponentViolationFlowRow
		var sevRank int
		if err := frows.Scan(
			&r.ProjectID, &r.ProjectSlug, &r.ProjectName, &r.Product,
			&r.FlowID, &r.FlowName, &r.ViolationCount, &sevRank,
		); err != nil {
			return ComponentViolationsAggregate{}, nil, err
		}
		r.HighestSeverity = severityRankToString(sevRank)
		out = append(out, r)
	}
	return agg, out, frows.Err()
}

func severityRankToString(rank int) string {
	switch rank {
	case 1:
		return "critical"
	case 2:
		return "high"
	case 3:
		return "medium"
	case 4:
		return "low"
	}
	return "info"
}
