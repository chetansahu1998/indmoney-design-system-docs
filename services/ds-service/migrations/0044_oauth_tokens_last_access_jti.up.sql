-- Plan 002 follow-up — /ce-code-review finding #8.
--
-- OAuth-minted access tokens were not enrolled in the existing JTI
-- revocation cache. Result: /v1/oauth/revoke marked the refresh row
-- revoked but the live access token kept working until its 1h TTL
-- expired. Refresh-token rotation had the same gap — the old access
-- token chain stayed valid until expiry.
--
-- Fix: record each minted access JTI on the refresh row that produced
-- it. /v1/oauth/revoke and the rotation path now look this up and
-- INSERT into revoked_jtis, so the middleware's IsJTIRevoked check
-- catches the next request from that access token (worst case: the
-- 60s in-memory cache TTL on revoked_jtis).
--
-- last_access_jti is NULL on authorization_code rows (they don't mint
-- access tokens directly) and on refresh_token rows that haven't been
-- used for rotation yet. Populated at mint + rotation time.

ALTER TABLE oauth_tokens ADD COLUMN last_access_jti TEXT;

-- No index — the column is only read via PK lookup of the parent row.
