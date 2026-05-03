package db

import (
	"context"
	"sync"
	"time"
)

// revocationCacheTTL is how long a "is this jti revoked?" answer stays in
// memory before we re-query SQLite. 60 s gives operations a predictable
// upper bound on how long a freshly-revoked token may still be honored
// (worst case: a request that just hit the cache before the revoke
// landed — that request keeps working until its cached entry expires).
//
// Tightening this would push the lookup cost onto every authenticated
// request; loosening it would widen the revocation window. 60 s is the
// same window we already accept for token-issuance latency etc.
const revocationCacheTTL = 60 * time.Second

type revocationEntry struct {
	revoked  bool
	expires  time.Time
}

var (
	revocationCache   sync.Map // jti string → revocationEntry
)

// IsJTIRevoked returns true when the JWT identified by `jti` has been
// added to revoked_jtis. Hits an in-memory cache (60 s TTL) so the
// happy path is a sync.Map lookup, not a SQL round-trip per request.
func (d *DB) IsJTIRevoked(ctx context.Context, jti string) bool {
	if jti == "" {
		// Defense in depth — a JWT with no jti claim was never minted by
		// our SigningKey (every mint sets uuid.NewString()), but if
		// someone forges one the safe behavior is "treat as not revoked"
		// because revocation can't apply to an unidentifiable token.
		// The signature check above this layer rejects forgeries first.
		return false
	}

	if cached, ok := revocationCache.Load(jti); ok {
		entry := cached.(revocationEntry)
		if time.Now().Before(entry.expires) {
			return entry.revoked
		}
	}

	// Cache miss or expired — single indexed PK lookup.
	var hit int
	err := d.QueryRowContext(ctx,
		`SELECT 1 FROM revoked_jtis WHERE jti = ?`, jti,
	).Scan(&hit)
	revoked := err == nil && hit == 1

	revocationCache.Store(jti, revocationEntry{
		revoked: revoked,
		expires: time.Now().Add(revocationCacheTTL),
	})
	return revoked
}

// RevokeJTI inserts a row into revoked_jtis so subsequent calls to
// IsJTIRevoked return true. Idempotent on the (jti) primary key.
// `revokedBy` and `reason` are advisory and free-form; persisted for
// audit / incident-review only.
func (d *DB) RevokeJTI(ctx context.Context, jti, revokedBy, reason string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO revoked_jtis (jti, revoked_at, revoked_by, reason)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(jti) DO UPDATE SET
		     revoked_at = excluded.revoked_at,
		     revoked_by = excluded.revoked_by,
		     reason = excluded.reason`,
		jti, time.Now().UTC().Format(time.RFC3339), revokedBy, reason,
	)
	if err == nil {
		// Invalidate any stale "not revoked" cache entry so the next
		// auth check sees the new state without waiting for TTL.
		revocationCache.Store(jti, revocationEntry{
			revoked: true,
			expires: time.Now().Add(revocationCacheTTL),
		})
	}
	return err
}
