-- 0047_oauth_clients.up.sql — Dynamic Client Registration (RFC 7591).
--
-- The Claude.ai Custom Connector flow doesn't use pre-shared client_ids.
-- Instead, when a user adds a connector, Claude.ai POSTs to our
-- `registration_endpoint` to mint its own client_id, then runs the
-- standard authorize+token dance with that client_id. Without a
-- registration endpoint, Claude's connector setup hits 401 on /mcp,
-- reads our discovery doc, finds no `registration_endpoint`, and gives
-- up — exactly the "Couldn't connect" failure shipped in plan 002.
--
-- This table backs the new POST /v1/oauth/register handler. Each row
-- is one client (Claude instance, third-party MCP-aware tool, etc.).
-- redirect_uris is exact-match at authorize-time per RFC 6749 §3.1.2.2
-- (same gate as the pre-DCR static client config).
--
-- Open registration: no auth on /v1/oauth/register. RFC 7591 allows this;
-- the security control is the redirect_uri allowlist on each individual
-- client. An attacker who registers a client can only siphon codes to
-- their own registered redirect_uri, which they already control —
-- there's nothing to steal.

CREATE TABLE IF NOT EXISTS oauth_clients (
    id                         TEXT PRIMARY KEY,                  -- the client_id we issue (UUID)
    client_name                TEXT NOT NULL DEFAULT '',           -- human label from the registration request
    redirect_uris              TEXT NOT NULL,                      -- JSON array of strings
    grant_types                TEXT NOT NULL DEFAULT '["authorization_code","refresh_token"]',
    response_types             TEXT NOT NULL DEFAULT '["code"]',
    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',       -- PKCE public client only
    scope                      TEXT NOT NULL DEFAULT '',
    contacts                   TEXT,                               -- JSON array, optional
    client_uri                 TEXT,
    logo_uri                   TEXT,
    tos_uri                    TEXT,
    policy_uri                 TEXT,
    software_id                TEXT,
    software_version           TEXT,
    created_at                 INTEGER NOT NULL,                   -- unix seconds
    last_used_at               INTEGER                             -- updated by authorize / token handlers
);

-- Cleanup hot path: a future reaper can drop clients that haven't been
-- used in N days. Indexed for efficient cutoff scans.
CREATE INDEX IF NOT EXISTS idx_oauth_clients_last_used
    ON oauth_clients(last_used_at);
