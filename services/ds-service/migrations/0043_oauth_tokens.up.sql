-- 0043_oauth_tokens.up.sql — Plan 002 U8.
--
-- OAuth 2.1 + PKCE authorization-code + refresh-token storage for the
-- MCP Connector flow. Three endpoints sit in front of this table
-- (`/v1/oauth/authorize`, `/v1/oauth/token`, `/v1/oauth/revoke`) — see
-- services/ds-service/internal/auth/oauth.go.
--
-- A single row models both kinds (authorization_code, refresh_token) so
-- the rotation path can revoke the old refresh row and INSERT the new
-- one atomically through one shared schema. The discriminator is `kind`.
--
-- ID convention:
--   - kind='authorization_code': id = UUIDv4 (the code itself; codes
--     are single-use 60s credentials, no need to hash them).
--   - kind='refresh_token':      id = hex(sha256(token_bytes)). The
--     raw 32-byte refresh token is base64url-encoded and returned to
--     the client; only its sha256 hash sits on disk. A leaked DB row
--     can't be replayed as a refresh token.
--
-- Cross-tenant safety (Rule 4 in CLAUDE.md): every row carries
-- `tenant_id` captured at authorize-time. The refresh path mints the
-- new access JWT using THIS column — never caller-supplied state — so
-- a refresh-token replay can never cross tenants.
--
-- Lifecycle columns:
--   expires_at  — UNIX seconds. Codes default to now+60s; refresh
--                  tokens default to now+30 days (OAuth 2.1 BCP cap).
--   consumed_at — set when an authorization_code is redeemed at
--                  /v1/oauth/token. Reuse → invalid_grant.
--   revoked_at  — set on /v1/oauth/revoke OR on rotation OR on
--                  replay-defense sweep. Once set, the row is dead.

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id             TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('authorization_code','refresh_token')),
    client_id             TEXT NOT NULL,
    redirect_uri          TEXT NOT NULL,
    scope                 TEXT NOT NULL DEFAULT '',
    code_challenge        TEXT,
    code_challenge_method TEXT,
    expires_at            INTEGER NOT NULL,
    consumed_at           INTEGER,
    revoked_at            INTEGER,
    created_at            INTEGER NOT NULL
);

-- User lookup is the replay-defense sweep path: when a refresh token is
-- reused, OAuth 2.1 BCP says revoke every live refresh token belonging
-- to that user. This index makes that sweep O(k) on the user's tokens.
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user_id
    ON oauth_tokens(user_id);

-- (kind, expires_at) supports the eventual reaper that purges expired
-- codes + tokens. Codes expire after 60s; without a sweep, the table
-- grows by one row per connector-add. Reaper isn't shipped here but the
-- index is cheap and removes a future migration.
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_kind_expires
    ON oauth_tokens(kind, expires_at);
