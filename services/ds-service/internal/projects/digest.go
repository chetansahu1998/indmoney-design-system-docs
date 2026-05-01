package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Phase 5 U8 — opt-in daily/weekly digest of unread notifications.
//
// The digest is built per-recipient + per-channel. Cadence resolves the
// firing window: 'daily' fires when user-local time crosses 09:00; 'weekly'
// fires Monday 09:00 user-local. The cron CLI invokes BuildAndSendDigest
// once an hour at :00 UTC; per-user TZ math determines whether to deliver
// for that user.
//
// A delivered notification has its delivered_via JSON array appended with
// the channel name so a re-run within the same window doesn't redeliver.
//
// Phase 5 ships Slack via incoming webhook (POST JSON to a stored URL) +
// email via SMTP relay (env SMTP_HOST + SMTP_FROM). When the relay env
// isn't configured, the email channel logs and skips — the in-app inbox
// is always the floor.

// Cadence values.
const (
	CadenceOff    = "off"
	CadenceDaily  = "daily"
	CadenceWeekly = "weekly"
)

// Channel values.
const (
	ChannelSlack = "slack"
	ChannelEmail = "email"
	ChannelInApp = "in_app" // floor channel; never opted out, never delivered via digest
)

// Sentinel errors.
var (
	ErrInvalidCadence = errors.New("digest: invalid cadence")
	ErrInvalidChannel = errors.New("digest: invalid channel")
)

// PreferenceInput is the shape upserted by SetNotificationPreference. Empty
// fields are treated as "leave as-is" if a row exists; the channel + user
// pair is the upsert key.
type PreferenceInput struct {
	UserID          string
	Channel         string
	Cadence         string
	SlackWebhookURL string
	EmailAddress    string
	UserTZ          string
}

// PreferenceRecord is the persisted shape returned by Get/List. Mirrors the
// JSON wire shape.
type PreferenceRecord struct {
	UserID          string    `json:"user_id"`
	Channel         string    `json:"channel"`
	Cadence         string    `json:"cadence"`
	SlackWebhookURL string    `json:"slack_webhook_url,omitempty"`
	EmailAddress    string    `json:"email_address,omitempty"`
	UserTZ          string    `json:"user_tz,omitempty"`
	LastDigestAt    time.Time `json:"last_digest_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ValidatePreferenceInput trims + checks the cadence/channel enums.
// Webhook URLs and email addresses are accepted as-is — Phase 7 admin
// hardening can validate the URL shape + DNS-resolve.
func ValidatePreferenceInput(in PreferenceInput) (PreferenceInput, error) {
	out := in
	out.UserID = strings.TrimSpace(in.UserID)
	out.Channel = strings.TrimSpace(in.Channel)
	out.Cadence = strings.TrimSpace(in.Cadence)
	out.UserTZ = strings.TrimSpace(in.UserTZ)
	out.SlackWebhookURL = strings.TrimSpace(in.SlackWebhookURL)
	out.EmailAddress = strings.TrimSpace(in.EmailAddress)

	switch out.Channel {
	case ChannelSlack, ChannelEmail:
		// ok
	default:
		return PreferenceInput{}, fmt.Errorf("%w: %q", ErrInvalidChannel, out.Channel)
	}
	switch out.Cadence {
	case CadenceOff, CadenceDaily, CadenceWeekly:
		// ok
	default:
		return PreferenceInput{}, fmt.Errorf("%w: %q", ErrInvalidCadence, out.Cadence)
	}
	return out, nil
}

// ListEligibleDigestPreferences returns every preference whose firing
// window matches `now` (per EligibleForDigest). Optional channel filter
// limits to one of "slack" / "email".
func (db *DB) ListEligibleDigestPreferences(ctx context.Context, now time.Time, channel string) ([]PreferenceRecord, error) {
	q := `SELECT user_id, channel, cadence,
	             COALESCE(slack_webhook_url, ''), COALESCE(email_address, ''),
	             COALESCE(user_tz, ''), COALESCE(last_digest_at, ''), updated_at
	        FROM notification_preferences
	       WHERE cadence != 'off'`
	args := []any{}
	if channel != "" {
		q += " AND channel = ?"
		args = append(args, channel)
	}
	rows, err := db.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list eligible: %w", err)
	}
	defer rows.Close()
	var out []PreferenceRecord
	for rows.Next() {
		rec := PreferenceRecord{}
		var lastDigest, updatedAt string
		if err := rows.Scan(
			&rec.UserID, &rec.Channel, &rec.Cadence,
			&rec.SlackWebhookURL, &rec.EmailAddress,
			&rec.UserTZ, &lastDigest, &updatedAt,
		); err != nil {
			return nil, err
		}
		rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		if lastDigest != "" {
			rec.LastDigestAt, _ = time.Parse(time.RFC3339Nano, lastDigest)
		}
		if EligibleForDigest(&rec, now) {
			out = append(out, rec)
		}
	}
	return out, rows.Err()
}

// UpsertNotificationPreference writes (or updates) a preferences row.
func (db *DB) UpsertNotificationPreference(ctx context.Context, in PreferenceInput) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.db.ExecContext(ctx,
		`INSERT INTO notification_preferences
		 (user_id, channel, cadence, slack_webhook_url, email_address, user_tz, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, channel) DO UPDATE SET
		   cadence = excluded.cadence,
		   slack_webhook_url = COALESCE(NULLIF(excluded.slack_webhook_url, ''), notification_preferences.slack_webhook_url),
		   email_address     = COALESCE(NULLIF(excluded.email_address, ''),    notification_preferences.email_address),
		   user_tz           = COALESCE(NULLIF(excluded.user_tz, ''),          notification_preferences.user_tz),
		   updated_at        = excluded.updated_at`,
		in.UserID, in.Channel, in.Cadence,
		in.SlackWebhookURL, in.EmailAddress, in.UserTZ,
		now,
	)
	return err
}

// GetNotificationPreference returns one preference row or nil + ErrNotFound.
func (db *DB) GetNotificationPreference(ctx context.Context, userID, channel string) (*PreferenceRecord, error) {
	row := db.db.QueryRowContext(ctx,
		`SELECT user_id, channel, cadence,
		        COALESCE(slack_webhook_url, ''), COALESCE(email_address, ''),
		        COALESCE(user_tz, ''), COALESCE(last_digest_at, ''), updated_at
		   FROM notification_preferences
		  WHERE user_id = ? AND channel = ?`,
		userID, channel,
	)
	rec := &PreferenceRecord{}
	var lastDigest, updatedAt string
	if err := row.Scan(
		&rec.UserID, &rec.Channel, &rec.Cadence,
		&rec.SlackWebhookURL, &rec.EmailAddress,
		&rec.UserTZ, &lastDigest, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get preference: %w", err)
	}
	rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if lastDigest != "" {
		rec.LastDigestAt, _ = time.Parse(time.RFC3339Nano, lastDigest)
	}
	return rec, nil
}

// EligibleForDigest reports whether `now` (UTC) falls inside the firing
// window for a given preference. Pure-function so the cron CLI can dry-run
// the schedule without touching the DB. The firing rule:
//
//   daily  → user-local time is exactly 09:00 hour AND last_digest_at is
//            absent OR more than 23 hours behind.
//   weekly → user-local time is Monday 09:00 hour AND last_digest_at is
//            absent OR more than 6 days behind.
//
// Falsy on every other case.
func EligibleForDigest(pref *PreferenceRecord, now time.Time) bool {
	if pref == nil {
		return false
	}
	if pref.Cadence == CadenceOff {
		return false
	}
	loc, err := time.LoadLocation(pref.UserTZ)
	if err != nil || pref.UserTZ == "" {
		loc = time.UTC
	}
	local := now.In(loc)
	if local.Hour() != 9 {
		return false
	}
	switch pref.Cadence {
	case CadenceDaily:
		if pref.LastDigestAt.IsZero() {
			return true
		}
		return now.Sub(pref.LastDigestAt) >= 23*time.Hour
	case CadenceWeekly:
		if local.Weekday() != time.Monday {
			return false
		}
		if pref.LastDigestAt.IsZero() {
			return true
		}
		return now.Sub(pref.LastDigestAt) >= 6*24*time.Hour
	}
	return false
}

// DigestPayload is the rendered shape we ship over Slack / email.
// Recipients see one block per flow; each block summarises the unread
// notifications grouped by kind with sample bodies.
type DigestPayload struct {
	UserID    string
	Channel   string
	Header    string
	FlowGroups []DigestFlowGroup
	GeneratedAt time.Time
}

// DigestFlowGroup is one flow's slice of unread notifications.
type DigestFlowGroup struct {
	FlowID    string
	FlowName  string
	Items     []DigestItem
}

// DigestItem is one row in a flow group.
type DigestItem struct {
	Kind    string
	Snippet string
	Actor   string
	TS      time.Time
}

// BuildDigestForUser assembles the payload from notifications that haven't
// been delivered via this channel yet. Deletes nothing on its own — the
// caller is expected to call MarkDelivered after a successful send.
func (db *DB) BuildDigestForUser(ctx context.Context, userID, channel string) (DigestPayload, []string, error) {
	rows, err := db.db.QueryContext(ctx,
		`SELECT n.id, n.kind, COALESCE(n.flow_id, ''), COALESCE(f.name, ''),
		        COALESCE(n.payload_json, ''), COALESCE(n.actor_user_id, ''),
		        n.created_at, COALESCE(n.delivered_via, '')
		   FROM notifications n
		   LEFT JOIN flows f ON f.id = n.flow_id
		  WHERE n.recipient_user_id = ?
		  ORDER BY n.created_at DESC
		  LIMIT 200`,
		userID,
	)
	if err != nil {
		return DigestPayload{}, nil, fmt.Errorf("digest fetch: %w", err)
	}
	defer rows.Close()

	groups := map[string]*DigestFlowGroup{}
	var ids []string
	for rows.Next() {
		var id, kind, flowID, flowName, payload, actor, deliveredVia, ts string
		if err := rows.Scan(&id, &kind, &flowID, &flowName, &payload, &actor, &ts, &deliveredVia); err != nil {
			return DigestPayload{}, nil, err
		}
		// Skip notifications already delivered via this channel.
		if deliveredVia != "" {
			var delivered []string
			if err := json.Unmarshal([]byte(deliveredVia), &delivered); err == nil {
				skip := false
				for _, c := range delivered {
					if c == channel {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			}
		}
		ids = append(ids, id)
		key := flowID
		if key == "" {
			key = "no-flow"
		}
		grp, ok := groups[key]
		if !ok {
			label := flowName
			if label == "" {
				label = "Other"
			}
			grp = &DigestFlowGroup{FlowID: flowID, FlowName: label}
			groups[key] = grp
		}
		snippet := ""
		if payload != "" {
			var obj map[string]any
			_ = json.Unmarshal([]byte(payload), &obj)
			if s, _ := obj["body_snippet"].(string); s != "" {
				snippet = s
			}
		}
		t, _ := time.Parse(time.RFC3339Nano, ts)
		grp.Items = append(grp.Items, DigestItem{
			Kind: kind, Snippet: snippet, Actor: actor, TS: t,
		})
	}

	out := DigestPayload{
		UserID:      userID,
		Channel:     channel,
		Header:      fmt.Sprintf("Your %s digest", channel),
		GeneratedAt: time.Now().UTC(),
	}
	for _, g := range groups {
		out.FlowGroups = append(out.FlowGroups, *g)
	}
	return out, ids, nil
}

// MarkDelivered appends `channel` to delivered_via for every id and bumps
// the user's last_digest_at on the preferences row.
func (db *DB) MarkDelivered(ctx context.Context, userID, channel string, notificationIDs []string) error {
	if len(notificationIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range notificationIDs {
		// Read existing delivered_via, append channel, write back. SQLite's
		// json_each + json_group_array would be cleaner, but a per-row
		// write is fine at this scale.
		var existing string
		_ = db.db.QueryRowContext(ctx,
			`SELECT COALESCE(delivered_via, '') FROM notifications WHERE id = ?`,
			id,
		).Scan(&existing)
		var arr []string
		if existing != "" {
			_ = json.Unmarshal([]byte(existing), &arr)
		}
		// Skip dupes.
		dup := false
		for _, c := range arr {
			if c == channel {
				dup = true
				break
			}
		}
		if !dup {
			arr = append(arr, channel)
		}
		bs, _ := json.Marshal(arr)
		if _, err := db.db.ExecContext(ctx,
			`UPDATE notifications SET delivered_via = ? WHERE id = ?`,
			string(bs), id,
		); err != nil {
			return fmt.Errorf("mark delivered: %w", err)
		}
	}
	_, err := db.db.ExecContext(ctx,
		`UPDATE notification_preferences SET last_digest_at = ? WHERE user_id = ? AND channel = ?`,
		now, userID, channel,
	)
	return err
}

// SlackSender posts a JSON payload to a Slack incoming webhook URL.
// Returns nil on 2xx, error otherwise. Caller is responsible for
// rendering payload.Text.
type SlackSender struct {
	HTTPClient *http.Client
}

// SlackMessage matches Slack's incoming-webhook JSON shape (text only —
// blocks/attachments are Phase 7 polish).
type SlackMessage struct {
	Text string `json:"text"`
}

// Send posts to the webhook URL.
func (s *SlackSender) Send(ctx context.Context, webhookURL string, msg SlackMessage) error {
	if webhookURL == "" {
		return errors.New("slack: empty webhook url")
	}
	bs, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, strings.NewReader(string(bs)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	return nil
}

// RenderSlackText is the simplest text-only digest render. Phase 7 admin
// can swap to Block-Kit for richer formatting.
func RenderSlackText(payload DigestPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s* — %d flow%s\n",
		payload.Header, len(payload.FlowGroups), pluralize(len(payload.FlowGroups)))
	for _, g := range payload.FlowGroups {
		fmt.Fprintf(&b, "\n• *%s* — %d update%s\n", g.FlowName, len(g.Items), pluralize(len(g.Items)))
		for _, it := range g.Items {
			fmt.Fprintf(&b, "  – %s", it.Kind)
			if it.Snippet != "" {
				fmt.Fprintf(&b, ": %s", it.Snippet)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func pluralize(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
