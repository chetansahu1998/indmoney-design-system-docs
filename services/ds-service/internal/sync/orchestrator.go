// Package sync orchestrates the end-to-end token sync flow:
//
//	1. Decrypt Figma PAT from DB.
//	2. Run pair-walker extractor (existing internal/figma/extractor).
//	3. Convert to DTCG via internal/figma/dtcg.
//	4. Compute canonical SHA-256 hash of the sorted DTCG output.
//	5. Compare to last sync's hash; if equal → skip-no-change.
//	6. Otherwise write JSON files to lib/tokens/<brand>/.
//	7. Optionally commit + push (controlled by SYNC_GIT_PUSH env flag).
//	8. Update sync_state + audit_log in DB.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/dtcg"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
)

// Orchestrator runs sync jobs.
type Orchestrator struct {
	DB      *db.DB
	Enc     *auth.EncryptionKey
	RepoDir string // absolute path to docs site repo (for writing tokens + git ops)
	Log     *slog.Logger
	GitPush bool // when true, commit + push on token change
}

// SyncResult is what we return to the API caller.
type SyncResult struct {
	TraceID         string    `json:"trace_id"`
	JobID           string    `json:"job_id"`
	Status          string    `json:"status"` // ok | skipped_nochange | failed
	CanonicalHash   string    `json:"canonical_hash"`
	StartedAt       time.Time `json:"started_at"`
	DurationMs      int       `json:"duration_ms"`
	Frames          int       `json:"frames"`
	Pairs           int       `json:"pairs"`
	Observations    int       `json:"observations"`
	Roles           int       `json:"roles"`
	BaseColors      int       `json:"base_colors"`
	CommittedSha    string    `json:"committed_sha,omitempty"`
	FailureMessage  string    `json:"failure_message,omitempty"`
	WrittenFiles    []string  `json:"written_files,omitempty"`
}

// Run executes the sync flow for one tenant.
//
// userID + traceID are stamped on the audit entry. brand maps to the
// lib/tokens/<brand>/ output directory (typically same as tenant slug).
func (o *Orchestrator) Run(ctx context.Context, tenantID, brand, userID, traceID string, sources []extractor.Source) (*SyncResult, error) {
	if traceID == "" {
		traceID = uuid.NewString()
	}
	jobID := uuid.NewString()
	started := time.Now()
	result := &SyncResult{
		TraceID:   traceID,
		JobID:     jobID,
		StartedAt: started,
	}
	failAndLog := func(err error) (*SyncResult, error) {
		result.Status = "failed"
		result.FailureMessage = err.Error()
		result.DurationMs = int(time.Since(started).Milliseconds())
		o.writeAudit(ctx, AuditCtx{
			Type:      "sync_failed",
			TenantID:  tenantID,
			UserID:    userID,
			TraceID:   traceID,
			Status:    500,
			Duration:  time.Since(started),
			Details:   err.Error(),
		})
		return result, err
	}

	// 1. Decrypt Figma PAT
	rec, err := o.DB.GetFigmaToken(ctx, tenantID)
	if err != nil {
		return failAndLog(fmt.Errorf("get figma token: %w", err))
	}
	patBytes, err := o.Enc.Decrypt(rec.EncryptedToken)
	if err != nil {
		return failAndLog(fmt.Errorf("decrypt figma token: %w", err))
	}
	pat := string(patBytes)
	o.Log.Info("figma token decrypted", "tenant", tenantID, "key_version", rec.KeyVersion)

	// 2. Run extractor
	c := client.New(pat)
	extractResult, err := extractor.Run(ctx, c, brand, sources, o.Log)
	if err != nil {
		return failAndLog(fmt.Errorf("extract: %w", err))
	}
	result.Frames = extractResult.CandidateCount()
	result.Pairs = extractResult.PairCount()
	result.Observations = len(extractResult.Observations)
	result.Roles = len(extractResult.Roles)
	result.BaseColors = len(extractResult.BasePalette)

	// 3. Convert to DTCG
	files, err := dtcg.Adapt(extractResult)
	if err != nil {
		return failAndLog(fmt.Errorf("dtcg adapt: %w", err))
	}

	// 4. Compute canonical hash (sorted JSON of all outputs)
	canonical, err := canonicalHash(files)
	if err != nil {
		return failAndLog(fmt.Errorf("hash: %w", err))
	}
	result.CanonicalHash = canonical
	o.Log.Info("canonical hash", "hash", canonical[:16]+"…")

	// 5. Skip-no-change?
	last, _ := o.DB.GetSyncState(ctx, tenantID)
	if last != nil && last.CanonicalHash == canonical {
		result.Status = "skipped_nochange"
		result.DurationMs = int(time.Since(started).Milliseconds())
		o.Log.Info("no change since last sync, skipping", "last_synced_at", last.LastSyncedAt)
		_ = o.DB.UpsertSyncState(ctx, db.SyncStateRecord{
			TenantID:         tenantID,
			CanonicalHash:    canonical,
			LastSyncedAt:     time.Now().UTC(),
			LastCommittedSha: last.LastCommittedSha,
			Status:           "skipped_nochange",
			UpdatedAt:        time.Now().UTC(),
		})
		o.writeAudit(ctx, AuditCtx{
			Type:     "sync_noop",
			TenantID: tenantID,
			UserID:   userID,
			TraceID:  traceID,
			Status:   200,
			Duration: time.Since(started),
			Details:  fmt.Sprintf(`{"canonical_hash":"%s"}`, canonical),
		})
		return result, nil
	}

	// 6. Write JSON files
	outDir := filepath.Join(o.RepoDir, "lib", "tokens", brand)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return failAndLog(fmt.Errorf("mkdir %s: %w", outDir, err))
	}
	written, err := writeFiles(outDir, files)
	if err != nil {
		return failAndLog(fmt.Errorf("write files: %w", err))
	}
	result.WrittenFiles = written
	o.Log.Info("wrote DTCG files", "count", len(written), "out", outDir)

	// 7. Optionally git commit + push
	committedSha := ""
	if o.GitPush {
		committedSha, err = o.gitCommitAndPush(ctx, brand, traceID, written)
		if err != nil {
			// Don't fail the whole sync — files are written, just not pushed.
			o.Log.Warn("git commit/push failed (files still written)", "err", err)
			result.FailureMessage = "git push failed: " + err.Error()
		}
	}
	result.CommittedSha = committedSha

	// 8. Update sync_state + audit
	now := time.Now().UTC()
	if err := o.DB.UpsertSyncState(ctx, db.SyncStateRecord{
		TenantID:         tenantID,
		CanonicalHash:    canonical,
		LastSyncedAt:     now,
		LastCommittedSha: committedSha,
		Status:           "ok",
		UpdatedAt:        now,
	}); err != nil {
		o.Log.Warn("upsert sync_state failed", "err", err)
	}

	result.Status = "ok"
	result.DurationMs = int(time.Since(started).Milliseconds())

	detailJSON, _ := json.Marshal(map[string]any{
		"canonical_hash":  canonical,
		"frames":          result.Frames,
		"pairs":           result.Pairs,
		"observations":    result.Observations,
		"roles":           result.Roles,
		"base_colors":     result.BaseColors,
		"committed_sha":   committedSha,
		"written_files":   written,
	})
	o.writeAudit(ctx, AuditCtx{
		Type:     "sync_ok",
		TenantID: tenantID,
		UserID:   userID,
		TraceID:  traceID,
		Status:   200,
		Duration: time.Since(started),
		Details:  string(detailJSON),
	})

	return result, nil
}

// AuditCtx is what writeAudit consumes.
type AuditCtx struct {
	Type     string
	TenantID string
	UserID   string
	TraceID  string
	Status   int
	Duration time.Duration
	Details  string // JSON
}

func (o *Orchestrator) writeAudit(ctx context.Context, a AuditCtx) {
	if err := o.DB.WriteAudit(ctx, db.AuditEntry{
		ID:         uuid.NewString(),
		TS:         time.Now(),
		EventType:  a.Type,
		TenantID:   a.TenantID,
		UserID:     a.UserID,
		Method:     "POST",
		Endpoint:   "/v1/sync/" + a.TenantID,
		StatusCode: a.Status,
		DurationMs: int(a.Duration.Milliseconds()),
		Details:    a.Details,
	}); err != nil {
		o.Log.Warn("audit write failed", "err", err)
	}
}

// canonicalHash computes a deterministic SHA-256 over the sorted JSON of
// base + semantic + semantic-dark. Text styles are excluded because their
// presence/absence shouldn't affect the skip-no-change decision.
func canonicalHash(files *dtcg.Files) (string, error) {
	parts := [][]byte{files.Base, files.Semantic, files.SemanticDark}
	h := sha256.New()
	for _, p := range parts {
		// Re-marshal to canonical form (sorted keys)
		var v any
		if err := json.Unmarshal(p, &v); err != nil {
			return "", err
		}
		canonical, err := canonicalJSON(v)
		if err != nil {
			return "", err
		}
		h.Write(canonical)
		h.Write([]byte{0}) // separator
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonicalJSON produces a deterministic JSON encoding (sorted keys, no whitespace).
func canonicalJSON(v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortStrings(keys)
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			kBytes, _ := json.Marshal(k)
			sb.Write(kBytes)
			sb.WriteByte(':')
			child, err := canonicalJSON(t[k])
			if err != nil {
				return nil, err
			}
			sb.Write(child)
		}
		sb.WriteByte('}')
		return []byte(sb.String()), nil
	case []any:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			child, err := canonicalJSON(x)
			if err != nil {
				return nil, err
			}
			sb.Write(child)
		}
		sb.WriteByte(']')
		return []byte(sb.String()), nil
	default:
		return json.Marshal(t)
	}
}

func sortStrings(s []string) {
	// In-place insertion sort — fine for small slices.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// writeFiles writes DTCG outputs into outDir. Returns relative paths written.
func writeFiles(outDir string, files *dtcg.Files) ([]string, error) {
	written := []string{}
	pairs := []struct {
		name    string
		content []byte
	}{
		{"base.tokens.json", files.Base},
		{"semantic.tokens.json", files.Semantic},
		{"semantic-dark.tokens.json", files.SemanticDark},
		{"_extraction-meta.json", files.ContractMeta},
	}
	if len(files.TextStyles) > 4 {
		pairs = append(pairs, struct {
			name    string
			content []byte
		}{"text-styles.tokens.json", files.TextStyles})
	}
	for _, p := range pairs {
		if len(p.content) == 0 {
			continue
		}
		path := filepath.Join(outDir, p.name)
		if err := os.WriteFile(path, p.content, 0o644); err != nil {
			return written, err
		}
		written = append(written, p.name)
	}
	return written, nil
}

// gitCommitAndPush commits the changed token files and pushes to origin.
// Caller's git config (user.name, user.email) must be set.
func (o *Orchestrator) gitCommitAndPush(ctx context.Context, brand, traceID string, files []string) (string, error) {
	tokensDir := filepath.Join("lib", "tokens", brand)
	cmds := [][]string{
		{"git", "-C", o.RepoDir, "add", tokensDir},
		{"git", "-C", o.RepoDir, "commit", "-m", fmt.Sprintf("chore(sync): tokens for %s (trace %s)", brand, traceID)},
		// 3-attempt retry on push (rebase against remote first to avoid races)
	}
	for _, args := range cmds {
		if err := runCmd(ctx, args[0], args[1:]...); err != nil {
			// `git commit` fails harmlessly when nothing to commit
			if strings.Contains(err.Error(), "nothing to commit") {
				o.Log.Info("nothing to commit, skipping push")
				return "", nil
			}
			return "", err
		}
	}
	// Get the new HEAD
	out, err := exec.CommandContext(ctx, "git", "-C", o.RepoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))

	// Push with rebase-retry
	for attempt := 0; attempt < 3; attempt++ {
		if err := runCmd(ctx, "git", "-C", o.RepoDir, "push", "origin", "HEAD"); err == nil {
			return sha, nil
		}
		o.Log.Warn("push failed, rebasing", "attempt", attempt+1)
		_ = runCmd(ctx, "git", "-C", o.RepoDir, "pull", "--rebase", "origin", "main")
		time.Sleep(time.Second * time.Duration(attempt+1))
	}
	return sha, fmt.Errorf("push failed after 3 attempts")
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (output: %s)", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}
