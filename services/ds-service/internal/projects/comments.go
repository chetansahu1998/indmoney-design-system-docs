package projects

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Phase 5 U6 — Comments + @mention parsing.
//
// Comments live on a universal target_kind / target_id pair so the same
// table backs DRD-block comments, decision comments, and violation
// comments without bespoke per-target tables. Phase 5 ships drd_block +
// decision + violation; screen + comment-replies-to-replies are 5.1
// polish.
//
// Mentions are parsed at write time from the body. The persisted format
// supports both BlockNote inline-mention nodes (the rich editor in U5)
// and plain `@username` strings (used by the Phase 5 minimal UI today).
// Both produce the same `mentions_user_ids` JSON array on disk.

// Length caps. Comments are short by design; longer narrative belongs
// in the DRD body, not the comment thread.
const (
	MaxCommentBodyLen = 4000
	MaxMentionsPerComment = 10
)

// CommentTargetKind enumerates the discriminator on drd_comments.target_kind.
type CommentTargetKind string

const (
	CommentTargetDRDBlock  CommentTargetKind = "drd_block"
	CommentTargetDecision  CommentTargetKind = "decision"
	CommentTargetViolation CommentTargetKind = "violation"
	CommentTargetScreen    CommentTargetKind = "screen"
	CommentTargetComment   CommentTargetKind = "comment" // reply target (depth-N polish; v1 ignores)
)

// Sentinel errors mapped to HTTP statuses by the handler layer.
var (
	ErrCommentBodyEmpty       = errors.New("comment: body required")
	ErrCommentBodyTooLong     = errors.New("comment: body exceeds cap")
	ErrCommentTargetUnknown   = errors.New("comment: unknown target_kind")
	ErrCommentTooManyMentions = errors.New("comment: too many @mentions")
)

// CommentInput is the validated, normalised input the repo trusts. The
// handler layer calls ValidateCommentInput first to centralise length /
// kind / mention checks.
type CommentInput struct {
	TargetKind      CommentTargetKind
	TargetID        string
	FlowID          string
	Body            string
	ParentCommentID string
	MentionedNames  []string // raw @names parsed from the body
}

// ValidateCommentInput runs the structural checks. Mention name validation
// (a name maps to a real user_id) happens at the repo layer where we
// have DB access. The validator just normalises the slice.
func ValidateCommentInput(in CommentInput) (CommentInput, error) {
	out := in

	out.Body = strings.TrimSpace(in.Body)
	if out.Body == "" {
		return CommentInput{}, ErrCommentBodyEmpty
	}
	if len(out.Body) > MaxCommentBodyLen {
		return CommentInput{}, ErrCommentBodyTooLong
	}

	switch out.TargetKind {
	case CommentTargetDRDBlock, CommentTargetDecision, CommentTargetViolation, CommentTargetScreen, CommentTargetComment:
		// ok
	default:
		return CommentInput{}, fmt.Errorf("%w: %q", ErrCommentTargetUnknown, in.TargetKind)
	}
	out.TargetID = strings.TrimSpace(out.TargetID)
	if out.TargetID == "" {
		return CommentInput{}, fmt.Errorf("%w: target_id required", ErrCommentTargetUnknown)
	}

	out.FlowID = strings.TrimSpace(out.FlowID)
	out.ParentCommentID = strings.TrimSpace(out.ParentCommentID)

	out.MentionedNames = parseMentionsFromText(out.Body)
	if len(out.MentionedNames) > MaxMentionsPerComment {
		return CommentInput{}, ErrCommentTooManyMentions
	}

	return out, nil
}

// mentionPattern matches @name segments (alphanumerics, dot, underscore,
// dash, ≥1 char). The trailing word boundary catches "@aanya" but not the
// middle of an email like "you@example.com" — we exclude when the previous
// rune is a non-mention char before the @.
var mentionPattern = regexp.MustCompile(`(?:^|[\s,.;!?(\[])@([a-zA-Z0-9._-]{1,40})`)

// parseMentionsFromText extracts unique @names (without the @). De-dups
// in declaration order so a reasonable upper bound applies regardless of
// how many times one name is mentioned.
//
// Trailing punctuation that's part of the regex character class (`.` `_`
// `-`) is stripped so "@karthik." captures "karthik". A name that's
// nothing but punctuation after stripping is dropped silently.
func parseMentionsFromText(body string) []string {
	matches := mentionPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.ToLower(strings.TrimRight(m[1], "._-"))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// CommentRecord is the persisted shape returned from the repo. Mirrors
// the JSON wire shape — snake_case, embedded mentions array.
type CommentRecord struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id"`
	TargetKind      string   `json:"target_kind"`
	TargetID        string   `json:"target_id"`
	FlowID          string   `json:"flow_id"`
	AuthorUserID    string   `json:"author_user_id"`
	Body            string   `json:"body"`
	ParentCommentID *string  `json:"parent_comment_id,omitempty"`
	MentionsUserIDs []string `json:"mentions_user_ids,omitempty"`
	ResolvedAt      string   `json:"resolved_at,omitempty"`
	ResolvedBy      string   `json:"resolved_by,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}
