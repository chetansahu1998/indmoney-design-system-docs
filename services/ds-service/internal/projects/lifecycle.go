package projects

import (
	"errors"
	"fmt"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// Phase 4 U1 — violation lifecycle. Pure-function transition table + actor-role
// gating, kept separate from repository.go so the rules can be unit-tested
// without a database.
//
// State machine (from the Phase 4 plan):
//
//   active → acknowledged    (any editor; reason required)
//   active → dismissed       (any editor; reason required)
//   active → fixed           (system-only — plugin auto-fix or re-export)
//   acknowledged → active    (admin only — DS-lead override)
//   dismissed → active       (admin only — DS-lead override)
//   acknowledged → fixed     (system-only — re-export resolves naturally)
//   dismissed    → fixed     (system-only — re-export resolves naturally)
//   fixed → *                (immutable — re-export creates fresh Active rows)

// ViolationStatus enumerates the persisted lifecycle states. Mirrors the
// `status` column DEFAULT in 0001_projects_schema.up.sql.
const (
	ViolationStatusActive       = "active"
	ViolationStatusAcknowledged = "acknowledged"
	ViolationStatusDismissed    = "dismissed"
	ViolationStatusFixed        = "fixed"
)

// LifecycleAction is what the PATCH endpoint accepts in the request body.
// "acknowledge" / "dismiss" / "reactivate" map to the status transitions
// above; the verb form is friendlier for plugin + UI clients than passing the
// target status directly.
type LifecycleAction string

const (
	ActionAcknowledge LifecycleAction = "acknowledge"
	ActionDismiss     LifecycleAction = "dismiss"
	ActionReactivate  LifecycleAction = "reactivate" // admin-only override (acknowledged|dismissed → active)
	ActionMarkFixed   LifecycleAction = "mark_fixed" // system-only (plugin auto-fix → fixed)
)

// MaxReasonLen mirrors the cap from the plan ("≤256 chars"); also matches the
// existing MaxStringLen used by the export validator.
const MaxReasonLen = 256

// Sentinel errors so the HTTP layer can map to the right status code without
// string-matching error messages.
var (
	ErrInvalidAction     = errors.New("lifecycle: unknown action")
	ErrInvalidTransition = errors.New("lifecycle: transition not allowed from this status")
	ErrReasonRequired    = errors.New("lifecycle: reason required for this action")
	ErrReasonTooLong     = errors.New("lifecycle: reason exceeds 256 chars")
	ErrForbiddenRole     = errors.New("lifecycle: role not permitted to take this action")
)

// LifecycleTransition is the pure result of validating an action against a
// current status + actor role. The repository layer applies it inside a
// transaction.
type LifecycleTransition struct {
	From    string          // current violations.status
	To      string          // resulting violations.status after the action
	Action  LifecycleAction // verb the caller supplied
	Reason  string          // trimmed reason (empty for system actions)
	System  bool            // true when the action is server-driven (mark_fixed)
}

// AuditEventType returns the audit_log.event_type slug for this transition.
// Persona-specific verbs make the log greppable ("violation.acknowledge"
// rather than the more generic "violation.status_changed").
func (t LifecycleTransition) AuditEventType() string {
	switch t.Action {
	case ActionAcknowledge:
		return "violation.acknowledge"
	case ActionDismiss:
		return "violation.dismiss"
	case ActionReactivate:
		return "violation.reactivate"
	case ActionMarkFixed:
		return "violation.mark_fixed"
	}
	return "violation.status_changed"
}

// ValidateTransition is the authoritative gate every PATCH /violations call
// must pass through. It does not touch the database — pure (status, action,
// role, reason) → transition or error.
//
// role is the auth.Claims.Role for the requesting user (super_admin |
// tenant_admin | designer | engineer | viewer). systemActor is true only for
// server-internal callers (the plugin auto-fix endpoint and the re-audit
// resolution path).
func ValidateTransition(currentStatus string, action LifecycleAction, role string, reason string, systemActor bool) (LifecycleTransition, error) {
	current := strings.ToLower(strings.TrimSpace(currentStatus))
	trimmedReason := strings.TrimSpace(reason)

	// Length-cap reasons regardless of action so the validator catches abuse
	// even when the action ends up being one that ignores the field.
	if len(trimmedReason) > MaxReasonLen {
		return LifecycleTransition{}, ErrReasonTooLong
	}

	switch action {
	case ActionAcknowledge:
		if current != ViolationStatusActive {
			return LifecycleTransition{}, fmt.Errorf("%w: %s → acknowledged", ErrInvalidTransition, current)
		}
		if trimmedReason == "" {
			return LifecycleTransition{}, ErrReasonRequired
		}
		return LifecycleTransition{
			From:   current,
			To:     ViolationStatusAcknowledged,
			Action: action,
			Reason: trimmedReason,
		}, nil

	case ActionDismiss:
		if current != ViolationStatusActive {
			return LifecycleTransition{}, fmt.Errorf("%w: %s → dismissed", ErrInvalidTransition, current)
		}
		if trimmedReason == "" {
			return LifecycleTransition{}, ErrReasonRequired
		}
		return LifecycleTransition{
			From:   current,
			To:     ViolationStatusDismissed,
			Action: action,
			Reason: trimmedReason,
		}, nil

	case ActionReactivate:
		if !isAdminRole(role) {
			return LifecycleTransition{}, ErrForbiddenRole
		}
		if current != ViolationStatusAcknowledged && current != ViolationStatusDismissed {
			return LifecycleTransition{}, fmt.Errorf("%w: %s → active", ErrInvalidTransition, current)
		}
		// Reason is optional on override but useful for the audit trail; pass through.
		return LifecycleTransition{
			From:   current,
			To:     ViolationStatusActive,
			Action: action,
			Reason: trimmedReason,
		}, nil

	case ActionMarkFixed:
		if !systemActor {
			return LifecycleTransition{}, ErrForbiddenRole
		}
		if current == ViolationStatusFixed {
			// Idempotent — already fixed. Caller can treat as no-op.
			return LifecycleTransition{}, fmt.Errorf("%w: already fixed", ErrInvalidTransition)
		}
		return LifecycleTransition{
			From:   current,
			To:     ViolationStatusFixed,
			Action: action,
			Reason: trimmedReason,
			System: true,
		}, nil
	}

	return LifecycleTransition{}, ErrInvalidAction
}

// isAdminRole returns true for roles that may flip Acknowledged / Dismissed
// back to Active. Phase 7 ACL grants will replace this with per-resource
// checks; for Phase 4 a single role gate is sufficient.
func isAdminRole(role string) bool {
	switch role {
	case auth.RoleSuperAdmin, auth.RoleTenantAdmin:
		return true
	}
	return false
}

// ParseLifecycleAction normalizes the request body's action string. Accepts
// case-insensitive verbs; rejects unknown values. Kept here so server.go and
// any future bulk endpoints share the same parser.
func ParseLifecycleAction(raw string) (LifecycleAction, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ActionAcknowledge):
		return ActionAcknowledge, nil
	case string(ActionDismiss):
		return ActionDismiss, nil
	case string(ActionReactivate):
		return ActionReactivate, nil
	case string(ActionMarkFixed):
		return ActionMarkFixed, nil
	}
	return "", ErrInvalidAction
}
