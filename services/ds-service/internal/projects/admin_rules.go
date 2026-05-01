package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Phase 7 U2 — rule catalog editor at /atlas/admin/rules.
//
// Super-admin gated. List handler returns every audit_rules row; patch
// handler updates `enabled` + `default_severity`. Editing a rule kicks
// off a fan-out re-audit (Phase 2 worker handles the actual run).
//
// We only expose the columns the editor needs — not auto_fixable / target
// node types (those stay in code) — so the wire surface is stable across
// rule definitions.

// AuditRuleRow is the read shape for the admin editor.
type AuditRuleRow struct {
	RuleID          string `json:"rule_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	DefaultSeverity string `json:"default_severity"`
	Enabled         bool   `json:"enabled"`
}

// listAuditRules reads the full rule catalog for the admin editor. Not
// tenant-scoped — the rule catalog is shared across all tenants. Caller
// must verify the user is a super-admin before invoking.
func listAuditRules(ctx context.Context, db *sql.DB) ([]AuditRuleRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT rule_id, name, description, category, default_severity, enabled
		   FROM audit_rules
		  ORDER BY category, rule_id`)
	if err != nil {
		return nil, fmt.Errorf("list audit_rules: %w", err)
	}
	defer rows.Close()
	var out []AuditRuleRow
	for rows.Next() {
		var r AuditRuleRow
		var enabledInt int
		if err := rows.Scan(&r.RuleID, &r.Name, &r.Description, &r.Category, &r.DefaultSeverity, &enabledInt); err != nil {
			return nil, fmt.Errorf("scan audit_rule: %w", err)
		}
		r.Enabled = enabledInt != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// patchAuditRule updates `enabled` and/or `default_severity` on a single
// rule. Returns ErrNotFound when the rule_id doesn't exist.
func patchAuditRule(ctx context.Context, db *sql.DB, ruleID string, enabled *bool, severity *string) error {
	if enabled == nil && severity == nil {
		return errors.New("admin_rules: nothing to update")
	}
	switch {
	case enabled != nil && severity != nil:
		v := 0
		if *enabled {
			v = 1
		}
		res, err := db.ExecContext(ctx,
			`UPDATE audit_rules SET enabled = ?, default_severity = ? WHERE rule_id = ?`,
			v, *severity, ruleID)
		if err != nil {
			return fmt.Errorf("patch rule: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
	case enabled != nil:
		v := 0
		if *enabled {
			v = 1
		}
		res, err := db.ExecContext(ctx,
			`UPDATE audit_rules SET enabled = ? WHERE rule_id = ?`, v, ruleID)
		if err != nil {
			return fmt.Errorf("patch rule enabled: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
	case severity != nil:
		res, err := db.ExecContext(ctx,
			`UPDATE audit_rules SET default_severity = ? WHERE rule_id = ?`, *severity, ruleID)
		if err != nil {
			return fmt.Errorf("patch rule severity: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// HandleAdminListRules serves GET /v1/atlas/admin/rules. Super-admin only.
func (s *Server) HandleAdminListRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if _, ok := s.requireAdminTenant(w, r); !ok {
		return
	}
	rules, err := listAuditRules(r.Context(), s.deps.DB.DB)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// HandleAdminPatchRule serves PATCH /v1/atlas/admin/rules/{rule_id}.
// Body: {"enabled"?: bool, "default_severity"?: string}
func (s *Server) HandleAdminPatchRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "PATCH only")
		return
	}
	if _, ok := s.requireAdminTenant(w, r); !ok {
		return
	}
	ruleID := r.PathValue("rule_id")
	if ruleID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_rule_id", "")
		return
	}
	var body struct {
		Enabled         *bool   `json:"enabled"`
		DefaultSeverity *string `json:"default_severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if body.DefaultSeverity != nil {
		switch *body.DefaultSeverity {
		case "critical", "high", "medium", "low", "info":
			// ok
		default:
			writeJSONErr(w, http.StatusBadRequest, "invalid_severity",
				"must be critical | high | medium | low | info")
			return
		}
	}
	err := patchAuditRule(r.Context(), s.deps.DB.DB, ruleID, body.Enabled, body.DefaultSeverity)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "rule_not_found", "")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "patch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
