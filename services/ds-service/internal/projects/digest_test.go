package projects

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 5 U8 — digest builder + eligibility tests.

func TestEligibleForDigest_Off(t *testing.T) {
	if EligibleForDigest(&PreferenceRecord{Cadence: CadenceOff}, time.Now()) {
		t.Error("off should never be eligible")
	}
}

func TestEligibleForDigest_DailyAt9LocalNoPriorDigest(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	// 09:30 IST → 04:00 UTC.
	now := time.Date(2026, 5, 2, 4, 0, 0, 0, time.UTC)
	pref := &PreferenceRecord{
		UserID: "u1", Channel: "slack", Cadence: CadenceDaily, UserTZ: loc.String(),
	}
	if !EligibleForDigest(pref, now) {
		t.Error("daily at 09:00 IST without prior digest should be eligible")
	}
}

func TestEligibleForDigest_DailyAt9LocalRecentDigest(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 5, 2, 4, 0, 0, 0, time.UTC)
	prior := now.Add(-2 * time.Hour) // 2h ago — too recent to fire again.
	pref := &PreferenceRecord{
		UserID: "u1", Channel: "slack", Cadence: CadenceDaily,
		UserTZ: loc.String(), LastDigestAt: prior,
	}
	if EligibleForDigest(pref, now) {
		t.Error("daily within 23h window should NOT fire")
	}
}

func TestEligibleForDigest_WeeklyMondayAt9Local(t *testing.T) {
	loc, _ := time.UTC, time.UTC
	_ = loc
	// Monday May 4, 2026 at 09:00 UTC.
	now := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	pref := &PreferenceRecord{
		UserID: "u1", Channel: "email", Cadence: CadenceWeekly, UserTZ: "UTC",
	}
	if !EligibleForDigest(pref, now) {
		t.Error("weekly Monday 09:00 UTC should be eligible")
	}
}

func TestEligibleForDigest_WeeklyOnTuesdayDoesNotFire(t *testing.T) {
	now := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC) // Tuesday
	pref := &PreferenceRecord{
		UserID: "u1", Channel: "email", Cadence: CadenceWeekly, UserTZ: "UTC",
	}
	if EligibleForDigest(pref, now) {
		t.Error("weekly should not fire on Tuesday")
	}
}

func TestValidatePreferenceInput_HappyPath(t *testing.T) {
	in, err := ValidatePreferenceInput(PreferenceInput{
		UserID: "u", Channel: "slack", Cadence: "daily",
		SlackWebhookURL: "https://hooks.slack.com/services/X/Y/Z",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if in.Channel != "slack" || in.Cadence != "daily" {
		t.Errorf("normalised wrong: %+v", in)
	}
}

func TestValidatePreferenceInput_RejectsBadCadence(t *testing.T) {
	_, err := ValidatePreferenceInput(PreferenceInput{
		UserID: "u", Channel: "slack", Cadence: "wat",
	})
	if !errors.Is(err, ErrInvalidCadence) {
		t.Errorf("expected ErrInvalidCadence, got %v", err)
	}
}

func TestValidatePreferenceInput_RejectsBadChannel(t *testing.T) {
	_, err := ValidatePreferenceInput(PreferenceInput{
		UserID: "u", Channel: "wat", Cadence: "daily",
	})
	if !errors.Is(err, ErrInvalidChannel) {
		t.Errorf("expected ErrInvalidChannel, got %v", err)
	}
}

func TestRenderSlackBlocks_HasHeaderContextSections(t *testing.T) {
	payload := DigestPayload{
		Header: "Your daily digest",
		FlowGroups: []DigestFlowGroup{
			{
				FlowName: "Tax / F&O",
				Items: []DigestItem{
					{Kind: "mention", Snippet: "ack — let's revisit"},
					{Kind: "decision_made"},
				},
			},
			{
				FlowName: "Plutus / Onboarding",
				Items: []DigestItem{
					{Kind: "mention", Snippet: "please confirm"},
				},
			},
		},
	}
	blocks := RenderSlackBlocks(payload)
	if len(blocks) < 5 {
		t.Fatalf("expected at least header+context+divider+2 sections+1 divider, got %d", len(blocks))
	}
	if blocks[0].Type != "header" || blocks[0].Text == nil ||
		blocks[0].Text.Text != "Your daily digest" {
		t.Errorf("first block should be header with payload header text, got %+v", blocks[0])
	}
	if blocks[1].Type != "context" || len(blocks[1].Elements) == 0 {
		t.Errorf("second block should be context with summary, got %+v", blocks[1])
	}
	// Find at least one section with the flow name + a bullet point.
	foundSection := false
	for _, b := range blocks {
		if b.Type == "section" && b.Text != nil &&
			strings.Contains(b.Text.Text, "Tax / F&O") &&
			strings.Contains(b.Text.Text, "ack — let's revisit") {
			foundSection = true
			break
		}
	}
	if !foundSection {
		t.Errorf("expected a section with Tax / F&O + snippet, got %+v", blocks)
	}
}

func TestRenderSlackBlocks_TruncatesAtMaxGroups(t *testing.T) {
	groups := make([]DigestFlowGroup, 12)
	for i := range groups {
		groups[i] = DigestFlowGroup{
			FlowName: "Flow " + string(rune('A'+i)),
			Items:    []DigestItem{{Kind: "mention"}},
		}
	}
	blocks := RenderSlackBlocks(DigestPayload{Header: "h", FlowGroups: groups})
	// Should include the trailing "…and N more flows" context block.
	last := blocks[len(blocks)-1]
	if last.Type != "context" || len(last.Elements) == 0 {
		t.Fatalf("expected trailing context block for truncation, got %+v", last)
	}
	if !strings.Contains(last.Elements[0].Text, "more flow") {
		t.Errorf("truncation message missing: %q", last.Elements[0].Text)
	}
}

func TestRenderSlackText_StructuresFlowGroups(t *testing.T) {
	payload := DigestPayload{
		Header: "Your daily digest",
		FlowGroups: []DigestFlowGroup{
			{
				FlowName: "Tax / F&O",
				Items: []DigestItem{
					{Kind: "mention", Snippet: "ack — let's revisit"},
					{Kind: "decision_made"},
				},
			},
		},
	}
	out := RenderSlackText(payload)
	if !strings.Contains(out, "Tax / F&O") || !strings.Contains(out, "mention") {
		t.Errorf("missing content: %q", out)
	}
}

func TestSlackSender_PostsAndFailsOn5xx(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	sender := &SlackSender{HTTPClient: srv.Client()}

	if err := sender.Send(context.Background(), srv.URL, SlackMessage{Text: "hello"}); err != nil {
		t.Errorf("first send: %v", err)
	}
	if err := sender.Send(context.Background(), srv.URL, SlackMessage{Text: "boom"}); err == nil {
		t.Errorf("second send should error on 500")
	}
}

func TestDB_DigestRoundTrip_PrefsAndDelivery(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repoDB := NewDB(d.DB)

	// Set a preference.
	if err := repoDB.UpsertNotificationPreference(context.Background(), PreferenceInput{
		UserID: uA, Channel: "slack", Cadence: "daily", SlackWebhookURL: "https://example.com/hook",
		UserTZ: "UTC",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	pref, err := repoDB.GetNotificationPreference(context.Background(), uA, "slack")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pref.Cadence != "daily" {
		t.Errorf("cadence: %q", pref.Cadence)
	}

	// Seed a notification for uA + mark delivered.
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	karthikID := seedTenantUser(t, d, tA, "karthik2@example.com")

	// Set karthik's preference + a webhook + UTC TZ.
	if err := repoDB.UpsertNotificationPreference(context.Background(), PreferenceInput{
		UserID: karthikID, Channel: "slack", Cadence: "daily",
		SlackWebhookURL: "https://example.com/hook", UserTZ: "UTC",
	}); err != nil {
		t.Fatalf("karthik upsert: %v", err)
	}

	// Generate a notification for karthik via @mention.
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "cc @karthik2",
	})
	if _, _, err := repo.CreateComment(context.Background(), uA, in); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	payload, ids, err := repoDB.BuildDigestForUser(context.Background(), karthikID, "slack")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 undelivered notification, got %d", len(ids))
	}
	if len(payload.FlowGroups) != 1 {
		t.Errorf("expected 1 flow group, got %d", len(payload.FlowGroups))
	}

	if err := repoDB.MarkDelivered(context.Background(), karthikID, "slack", ids); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	// Re-build → no undelivered notifications, last_digest_at populated.
	_, ids2, _ := repoDB.BuildDigestForUser(context.Background(), karthikID, "slack")
	if len(ids2) != 0 {
		t.Errorf("re-build should have 0 undelivered, got %d", len(ids2))
	}
	pref2, _ := repoDB.GetNotificationPreference(context.Background(), karthikID, "slack")
	if pref2.LastDigestAt.IsZero() {
		t.Errorf("last_digest_at should be set after delivery")
	}
}
