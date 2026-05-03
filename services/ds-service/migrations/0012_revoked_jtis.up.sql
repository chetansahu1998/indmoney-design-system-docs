-- 0012_revoked_jtis — T1 (audit follow-up plan 2026-05-03-001).
--
-- Per-token revocation list. requireAuth checks every JWT's `jti` claim
-- against this table after signature verification; a hit returns 401
-- token_revoked. Lets ops disable a leaked or stale token without
-- rotating JWT_SIGNING_KEY (which would invalidate every token —
-- including the one used to mint replacements).
--
-- jti is the primary key (RFC 7519 §4.1.7 — JWT ID). Our SigningKey
-- mints uuid.NewString() into RegisteredClaims.ID so jtis are
-- already unique across mints; no auxiliary uniqueness needed.
--
-- revoked_by stores the operator's user_id (free-form TEXT — no FK
-- because revoke-token CLI may run from a Fly SSH session where no
-- user_id is available; the CLI defaults to "ops" in that case).
--
-- reason is free-form context for incident review ("leaked on slack",
-- "former designer", "session was on a stolen laptop", etc.).

CREATE TABLE IF NOT EXISTS revoked_jtis (
    jti          TEXT PRIMARY KEY,
    revoked_at   TEXT NOT NULL,
    revoked_by   TEXT,
    reason       TEXT
);
