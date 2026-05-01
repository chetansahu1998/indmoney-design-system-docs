package projects

import (
	"errors"
)

// Phase 5 U7 — Notifications.
//
// One row per (recipient, event). Created in the same DB transaction as the
// underlying event (comment / decision / DRD edit) so the notification can
// never "miss" — the audit_log row + the notifications row(s) are atomic.
//
// Delivery channels (in_app / slack / email) track ACK in delivered_via.
// The in-app inbox channel auto-acks on read (read_at != NULL); slack +
// email are appended to delivered_via by the digest job.

// NotificationKind enumerates the kind discriminator on the row.
type NotificationKind string

const (
	NotifMention            NotificationKind = "mention"
	NotifDecisionMade       NotificationKind = "decision_made"
	NotifDecisionSuperseded NotificationKind = "decision_superseded"
	NotifCommentResolved    NotificationKind = "comment_resolved"
	NotifDRDEdited          NotificationKind = "drd_edited_on_owned_flow"
)

// NotificationTargetKind discriminates target_id (separate enum from
// CommentTargetKind because notifications can point at things comments
// can't, e.g., a 'drd' kind targeting the flow itself).
type NotificationTargetKind string

const (
	NotifTargetComment   NotificationTargetKind = "comment"
	NotifTargetDecision  NotificationTargetKind = "decision"
	NotifTargetDRD       NotificationTargetKind = "drd"
	NotifTargetViolation NotificationTargetKind = "violation"
)

// Sentinel errors.
var (
	ErrNotifKindUnknown   = errors.New("notification: unknown kind")
	ErrNotifTargetUnknown = errors.New("notification: unknown target_kind")
)

// NotificationInput is the structured shape callers hand to EmitNotification.
// The repo wraps this in an INSERT inside the same transaction as the
// triggering event.
type NotificationInput struct {
	RecipientUserID string
	Kind            NotificationKind
	TargetKind      NotificationTargetKind
	TargetID        string
	FlowID          string
	ActorUserID     string
	PayloadJSON     []byte // kind-specific JSON the UI uses for inline rendering
}

// NotificationRecord is the persisted shape returned to the inbox UI.
type NotificationRecord struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id"`
	RecipientUserID string   `json:"recipient_user_id"`
	Kind            string   `json:"kind"`
	TargetKind      string   `json:"target_kind,omitempty"`
	TargetID        string   `json:"target_id,omitempty"`
	FlowID          string   `json:"flow_id,omitempty"`
	ActorUserID     string   `json:"actor_user_id,omitempty"`
	PayloadJSON     []byte   `json:"payload_json,omitempty"`
	DeliveredVia    []string `json:"delivered_via,omitempty"`
	ReadAt          string   `json:"read_at,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

// ValidateNotificationKind / Target are tiny helpers the handler layer
// uses when parsing query strings; the emit path doesn't need them
// because callers construct the constants directly.
func ValidateNotificationKind(k string) (NotificationKind, error) {
	switch NotificationKind(k) {
	case NotifMention, NotifDecisionMade, NotifDecisionSuperseded, NotifCommentResolved, NotifDRDEdited:
		return NotificationKind(k), nil
	}
	return "", ErrNotifKindUnknown
}

func ValidateNotificationTargetKind(k string) (NotificationTargetKind, error) {
	switch NotificationTargetKind(k) {
	case NotifTargetComment, NotifTargetDecision, NotifTargetDRD, NotifTargetViolation:
		return NotificationTargetKind(k), nil
	}
	return "", ErrNotifTargetUnknown
}
