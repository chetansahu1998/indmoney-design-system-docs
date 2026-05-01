package projects

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Phase 5 U3 — Decisions as a first-class entity.
//
// Decisions are flow-scoped + version-anchored: a decision made on v1 stays
// attached to v1 even after v2 ships. The lifecycle is small (proposed →
// accepted; accepted → superseded happens only via a NEW decision pointing
// back via supersedes_id) so the validators here are simpler than
// lifecycle.go's violation state machine — but the supersession-cycle check
// is the load-bearing piece that protects the chain integrity.

// DecisionStatus enumerates the persisted statuses. Mirrors the column
// DEFAULT in 0008_decisions_comments_notifications.up.sql.
const (
	DecisionStatusProposed   = "proposed"
	DecisionStatusAccepted   = "accepted"
	DecisionStatusSuperseded = "superseded"
)

// Length caps + identifier limits. Match the conventions in
// internal/projects/server.go (MaxStringLen=256) so validators stay
// uniform across endpoints.
const (
	MaxDecisionTitleLen      = 200
	MaxDecisionBodyBytes     = 64 * 1024 // 64KB; way bigger than the typical paragraph
	MaxDecisionLinksPerWrite = 50
)

// LinkType discriminates the target_id namespace on decision_links. Phase 5
// ships violation + screen + component + external; the schema is open to
// future kinds (e.g., 'design_token').
type LinkType string

const (
	LinkTypeViolation LinkType = "violation"
	LinkTypeScreen    LinkType = "screen"
	LinkTypeComponent LinkType = "component"
	LinkTypeExternal  LinkType = "external"
)

// Sentinel errors mapped to HTTP statuses by the handler layer.
var (
	ErrDecisionTitleEmpty    = errors.New("decision: title required")
	ErrDecisionTitleTooLong  = errors.New("decision: title exceeds cap")
	ErrDecisionBodyTooLarge  = errors.New("decision: body exceeds cap")
	ErrDecisionInvalidStatus = errors.New("decision: invalid status")
	ErrDecisionCycle         = errors.New("decision: supersession would create a cycle")
	ErrDecisionLinkUnknown   = errors.New("decision: unknown link_type")
	ErrDecisionTooManyLinks  = errors.New("decision: too many links per request")
)

// DecisionInput is the validated, normalised shape callers hand to
// CreateDecision / UpdateDecision. All string trimming + length checks
// happen in ValidateDecisionInput; the repository layer trusts the
// returned struct.
type DecisionInput struct {
	Title         string
	BodyJSON      []byte // BlockNote JSON; nil OK
	Status        string
	SupersedesID  string
	Links         []DecisionLinkInput
}

// DecisionLinkInput is one row to write into decision_links alongside the
// decision insert. Pure data — no FK validation here (the SQL layer
// catches dangling target_ids via the parent table's FK or a 404 from
// the lookup helper).
type DecisionLinkInput struct {
	LinkType LinkType
	TargetID string
}

// ValidateDecisionInput normalises + checks a DecisionInput. Returns the
// trimmed title + accepted status + a sanitised links slice. Pure
// function — no DB access — so handler tests don't need a SQLite fixture.
func ValidateDecisionInput(in DecisionInput) (DecisionInput, error) {
	out := DecisionInput{
		BodyJSON: in.BodyJSON,
	}

	out.Title = strings.TrimSpace(in.Title)
	if out.Title == "" {
		return DecisionInput{}, ErrDecisionTitleEmpty
	}
	if len(out.Title) > MaxDecisionTitleLen {
		return DecisionInput{}, ErrDecisionTitleTooLong
	}
	if len(in.BodyJSON) > MaxDecisionBodyBytes {
		return DecisionInput{}, ErrDecisionBodyTooLarge
	}

	switch strings.TrimSpace(in.Status) {
	case "":
		out.Status = DecisionStatusAccepted // sensible default per the plan
	case DecisionStatusProposed, DecisionStatusAccepted:
		out.Status = strings.TrimSpace(in.Status)
	case DecisionStatusSuperseded:
		// 'superseded' is server-driven only — a NEW decision's
		// supersedes_id moves the predecessor to this status; callers
		// can't write it directly.
		return DecisionInput{}, fmt.Errorf("%w: superseded is server-driven", ErrDecisionInvalidStatus)
	default:
		return DecisionInput{}, ErrDecisionInvalidStatus
	}

	out.SupersedesID = strings.TrimSpace(in.SupersedesID)

	if len(in.Links) > MaxDecisionLinksPerWrite {
		return DecisionInput{}, ErrDecisionTooManyLinks
	}
	out.Links = make([]DecisionLinkInput, 0, len(in.Links))
	for _, l := range in.Links {
		t := LinkType(strings.TrimSpace(string(l.LinkType)))
		switch t {
		case LinkTypeViolation, LinkTypeScreen, LinkTypeComponent, LinkTypeExternal:
			// ok
		default:
			return DecisionInput{}, fmt.Errorf("%w: %q", ErrDecisionLinkUnknown, l.LinkType)
		}
		target := strings.TrimSpace(l.TargetID)
		if target == "" {
			continue // silently drop empty targets — caller is liberal here
		}
		out.Links = append(out.Links, DecisionLinkInput{LinkType: t, TargetID: target})
	}

	return out, nil
}

// CycleCheckHop is a single (id, supersedes_id) pair used by
// DetectSupersessionCycle to walk the chain backward without round-tripping
// the DB per hop. Caller pre-loads the chain into a map keyed by id.
type CycleCheckHop struct {
	ID            string
	SupersedesID  string
}

// DetectSupersessionCycle walks the chain starting at startID via the
// supplied hop map. Returns true when the new decision's `proposedID`
// would create a cycle (i.e., proposedID appears anywhere in the chain
// reachable from startID via supersedes_id pointers).
//
// Caller pre-loads `chain` from the same flow (cycles can only happen
// within a flow because supersedes references are flow-local — but the
// schema doesn't enforce that; the handler validates it).
func DetectSupersessionCycle(proposedID, startID string, chain map[string]CycleCheckHop) bool {
	if proposedID == "" || startID == "" {
		return false
	}
	visited := make(map[string]struct{}, len(chain))
	cur := startID
	for cur != "" {
		if cur == proposedID {
			return true
		}
		if _, seen := visited[cur]; seen {
			return true // existing data is already cyclic — refuse to extend it
		}
		visited[cur] = struct{}{}
		hop, ok := chain[cur]
		if !ok {
			return false
		}
		cur = hop.SupersedesID
	}
	return false
}

// MakeDecisionAuditEvent returns the audit_log.event_type slug for a
// decision lifecycle transition. Keeps the log greppable by verb.
func MakeDecisionAuditEvent(transition string) string {
	switch transition {
	case "create":
		return "decision.created"
	case "supersede":
		return "decision.superseded"
	case "delete":
		return "decision.deleted"
	case "update":
		return "decision.updated"
	}
	return "decision." + transition
}

// DecisionRecord is the persisted shape returned by the repo. The handler
// layer marshals this to JSON; the JSON shape carries snake_case field
// names so frontend types match the wire on first read.
type DecisionRecord struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	FlowID          string    `json:"flow_id"`
	VersionID       string    `json:"version_id"`
	Title           string    `json:"title"`
	BodyJSON        []byte    `json:"body_json,omitempty"`
	Status          string    `json:"status"`
	MadeByUserID    string    `json:"made_by_user_id"`
	MadeAt          time.Time `json:"made_at"`
	SupersededByID  *string   `json:"superseded_by_id,omitempty"`
	SupersedesID    *string   `json:"supersedes_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Links           []DecisionLink `json:"links,omitempty"`
}

// DecisionLink is one persisted row from decision_links. Returned alongside
// the parent decision so the UI can render link cards without a follow-up
// fetch.
type DecisionLink struct {
	DecisionID string    `json:"decision_id"`
	LinkType   LinkType  `json:"link_type"`
	TargetID   string    `json:"target_id"`
	CreatedAt  time.Time `json:"created_at"`
}
