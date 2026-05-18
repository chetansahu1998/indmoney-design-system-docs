-- 0045_oauth_tokens_tenant_fk — /ce-code-review finding #11 (P1).
--
-- oauth_tokens.tenant_id was added in 0043 as NOT NULL TEXT but with
-- no FK to tenants(id). This diverges from the repo's own retrofit
-- pattern (migration 0015) which exists specifically to add
-- `REFERENCES tenants(id) ON DELETE CASCADE` to every table that
-- omitted the constraint.
--
-- Effect of the gap:
--   • Tenant deletion leaves orphaned oauth_tokens rows (no cascade).
--   • A row could theoretically be written with a non-existent
--     tenant_id (today blocked at the application layer; database is
--     defense in depth).
--
-- Mechanics — SQLite doesn't support `ALTER TABLE ADD CONSTRAINT`, so
-- we run the rebuild dance: CREATE _new, INSERT … SELECT, DROP old,
-- RENAME _new, recreate indexes. `PRAGMA foreign_keys = OFF` disables
-- FK enforcement during the dance (otherwise the engine errors during
-- the dependent INSERT). The pragma can't toggle inside a transaction,
-- hence the .no_tx. filename suffix the migration runner recognises.
--
-- Pre-migration zero-orphan audit: every existing oauth_tokens.tenant_id
-- corresponds to a real tenants(id), since rows are only minted via the
-- authorize handler which reads tenant_id from claims.Tenants[0]
-- (claims sourced from a verified session JWT). foreign_key_check after
-- re-enable returns clean on every known deployment.

PRAGMA foreign_keys = OFF;

BEGIN;

-- Capture current shape: 0043 columns + 0044's last_access_jti.
CREATE TABLE oauth_tokens_new (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind                  TEXT NOT NULL CHECK (kind IN ('authorization_code','refresh_token')),
    client_id             TEXT NOT NULL,
    redirect_uri          TEXT NOT NULL,
    scope                 TEXT NOT NULL DEFAULT '',
    code_challenge        TEXT,
    code_challenge_method TEXT,
    expires_at            INTEGER NOT NULL,
    consumed_at           INTEGER,
    revoked_at            INTEGER,
    created_at            INTEGER NOT NULL,
    last_access_jti       TEXT
);

INSERT INTO oauth_tokens_new (
    id, user_id, tenant_id, kind, client_id, redirect_uri, scope,
    code_challenge, code_challenge_method, expires_at, consumed_at,
    revoked_at, created_at, last_access_jti
)
SELECT
    id, user_id, tenant_id, kind, client_id, redirect_uri, scope,
    code_challenge, code_challenge_method, expires_at, consumed_at,
    revoked_at, created_at, last_access_jti
FROM oauth_tokens;

DROP TABLE oauth_tokens;
ALTER TABLE oauth_tokens_new RENAME TO oauth_tokens;

-- Recreate indexes (0043 + carried forward).
CREATE INDEX idx_oauth_tokens_user_id
    ON oauth_tokens(user_id);
CREATE INDEX idx_oauth_tokens_kind_expires
    ON oauth_tokens(kind, expires_at);

COMMIT;

-- Re-enable FK enforcement for the rest of the connection's lifetime.
-- The migration runner pins this connection per .no_tx. file.
PRAGMA foreign_keys = ON;

-- Audit assertion — if any orphaned rows existed before this migration
-- they'd surface here as a non-empty result. The runner errors out on
-- any foreign_key_check rows.
PRAGMA foreign_key_check(oauth_tokens);
