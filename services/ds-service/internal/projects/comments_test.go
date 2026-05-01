package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// Phase 5 U6 — comments + mention parser tests.

func TestParseMentionsFromText_HappyPath(t *testing.T) {
	body := "Hey @aanya can you take a look? cc @karthik. @aanya again."
	got := parseMentionsFromText(body)
	want := []string{"aanya", "karthik"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("[%d] got %q, want %q", i, got[i], n)
		}
	}
}

func TestParseMentionsFromText_IgnoresEmailMidWord(t *testing.T) {
	body := "Email me at hello@example.com"
	got := parseMentionsFromText(body)
	if len(got) != 0 {
		t.Errorf("expected no mentions inside email, got %v", got)
	}
}

func TestParseMentionsFromText_DedupsCaseInsensitive(t *testing.T) {
	body := "@Karthik @karthik @KARTHIK"
	got := parseMentionsFromText(body)
	if len(got) != 1 || got[0] != "karthik" {
		t.Errorf("dedup failed: %v", got)
	}
}

func TestValidateCommentInput_HappyPath(t *testing.T) {
	in, err := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock,
		TargetID:   "block-1",
		FlowID:     "flow-1",
		Body:       "Looks good @aanya",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(in.MentionedNames) != 1 || in.MentionedNames[0] != "aanya" {
		t.Errorf("mentions parsed wrong: %v", in.MentionedNames)
	}
}

func TestValidateCommentInput_RejectsEmptyBody(t *testing.T) {
	_, err := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "x",
	})
	if !errors.Is(err, ErrCommentBodyEmpty) {
		t.Errorf("expected ErrCommentBodyEmpty, got %v", err)
	}
}

func TestValidateCommentInput_RejectsBigBody(t *testing.T) {
	_, err := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "x",
		Body: strings.Repeat("x", MaxCommentBodyLen+1),
	})
	if !errors.Is(err, ErrCommentBodyTooLong) {
		t.Errorf("expected ErrCommentBodyTooLong, got %v", err)
	}
}

func TestValidateCommentInput_RejectsUnknownTargetKind(t *testing.T) {
	_, err := ValidateCommentInput(CommentInput{
		TargetKind: "wat", TargetID: "x", Body: "hi",
	})
	if !errors.Is(err, ErrCommentTargetUnknown) {
		t.Errorf("expected ErrCommentTargetUnknown, got %v", err)
	}
}

func TestValidateCommentInput_RejectsTooManyMentions(t *testing.T) {
	body := strings.Repeat("@u ", MaxMentionsPerComment+2)
	// All @u dedup to one — to actually exceed the cap we need distinct names.
	body = ""
	for i := 0; i < MaxMentionsPerComment+2; i++ {
		body += " @u" + string(rune('a'+i))
	}
	_, err := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "x", Body: body,
	})
	if !errors.Is(err, ErrCommentTooManyMentions) {
		t.Errorf("expected ErrCommentTooManyMentions, got %v", err)
	}
}

// ─── Repo integration ───────────────────────────────────────────────────────

func seedTenantUser(t *testing.T, d *db.DB, tenantID, email string) string {
	t.Helper()
	uid := uuid.NewString()
	if err := d.CreateUser(context.Background(), db.User{
		ID: uid, Email: email, PasswordHash: "x", Role: "user", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := d.DB.Exec(
		`INSERT INTO tenant_users (tenant_id, user_id, role, status, created_at) VALUES (?, ?, 'designer', 'active', ?)`,
		tenantID, uid, time.Now().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("attach tenant_user: %v", err)
	}
	return uid
}

func TestRepo_CreateComment_NoMentions(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "Looks great",
	})
	rec, mentioned, err := repo.CreateComment(context.Background(), uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(mentioned) != 0 {
		t.Errorf("expected no mentions, got %v", mentioned)
	}
	if rec.Body != "Looks great" {
		t.Errorf("body round-trip: %q", rec.Body)
	}
}

func TestRepo_CreateComment_ResolvesMentionToUserID(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	karthikID := seedTenantUser(t, d, tA, "karthik@example.com")

	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "cc @karthik please",
	})
	rec, mentioned, err := repo.CreateComment(context.Background(), uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(mentioned) != 1 || mentioned[0] != karthikID {
		t.Errorf("expected karthik mentioned, got %v", mentioned)
	}

	// Notification row must exist.
	notifs, err := repo.ListNotificationsForUser(context.Background(), karthikID, false, 10)
	if err != nil {
		t.Fatalf("list notifs: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].Kind != string(NotifMention) {
		t.Errorf("kind: %q", notifs[0].Kind)
	}
	if notifs[0].TargetID != rec.ID {
		t.Errorf("target_id: %q", notifs[0].TargetID)
	}
}

func TestRepo_CreateComment_DropsSelfMention(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	// uA's email prefix (set in newTestDB) is "a". Comment includes "@a".
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "I'll take this @a",
	})
	_, mentioned, err := repo.CreateComment(context.Background(), uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range mentioned {
		if id == uA {
			t.Errorf("self-mention should have been dropped")
		}
	}
}

func TestRepo_CreateComment_CrossTenantMentionIgnored(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	// tenant B has a user "outside@b.com"; tenant A author tries to @them.
	_ = seedTenantUser(t, d, tB, "outside@b.com")

	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "cc @outside",
	})
	_, mentioned, err := repo.CreateComment(context.Background(), uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(mentioned) != 0 {
		t.Errorf("cross-tenant mention should be ignored, got %v", mentioned)
	}
}

func TestRepo_ListCommentsForTarget_ChronOrder(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	for _, body := range []string{"first", "second", "third"} {
		in, _ := ValidateCommentInput(CommentInput{
			TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
			Body: body,
		})
		if _, _, err := repo.CreateComment(context.Background(), uA, in); err != nil {
			t.Fatalf("create %q: %v", body, err)
		}
	}
	got, err := repo.ListCommentsForTarget(context.Background(), CommentTargetDRDBlock, "block-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// Bodies are unique enough; order should be ASC by created_at.
	if got[0].Body != "first" || got[2].Body != "third" {
		t.Errorf("order wrong: %+v", got)
	}
}

func TestRepo_ResolveComment_Idempotent(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "xy",
	})
	rec, _, _ := repo.CreateComment(context.Background(), uA, in)

	if err := repo.ResolveComment(context.Background(), rec.ID, uA); err != nil {
		t.Fatalf("resolve 1: %v", err)
	}
	if err := repo.ResolveComment(context.Background(), rec.ID, uA); err != nil {
		t.Errorf("resolve 2 should be idempotent, got %v", err)
	}
}

func TestRepo_ResolveComment_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repoA, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "x", FlowID: flowID, Body: "x",
	})
	rec, _, _ := repoA.CreateComment(context.Background(), uA, in)

	repoB := NewTenantRepo(d.DB, tB)
	if err := repoB.ResolveComment(context.Background(), rec.ID, uA); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRepo_NotificationsMarkRead_FiltersOtherUsers(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	karthikID := seedTenantUser(t, d, tA, "karthik@example.com")

	// Generate a mention notification for karthik.
	in, _ := ValidateCommentInput(CommentInput{
		TargetKind: CommentTargetDRDBlock, TargetID: "block-1", FlowID: flowID,
		Body: "cc @karthik",
	})
	if _, _, err := repo.CreateComment(context.Background(), uA, in); err != nil {
		t.Fatalf("create: %v", err)
	}

	notifs, _ := repo.ListNotificationsForUser(context.Background(), karthikID, true, 10)
	if len(notifs) != 1 {
		t.Fatalf("expected 1 unread, got %d", len(notifs))
	}

	// Mark as read by karthik → flips.
	n, err := repo.MarkNotificationsRead(context.Background(), karthikID, []string{notifs[0].ID})
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row flipped, got %d", n)
	}

	// Mark as read by uA (not the recipient) → 0 (filtered by recipient).
	n2, err := repo.MarkNotificationsRead(context.Background(), uA, []string{notifs[0].ID})
	if err != nil {
		t.Fatalf("mark read 2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 (cross-recipient filter), got %d", n2)
	}
}
