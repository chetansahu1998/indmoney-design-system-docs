-- 0046_oauth_tokens_parent_id — /ce-code-review finding #7 (P1).
--
-- The refresh-replay defense (OAuth 2.1 BCP §4.12.2) was sweeping
-- EVERY live refresh token belonging to the user when a replay was
-- detected. Behaviorally correct against a worst-case attacker who
-- has all of the user's tokens — but a single leaked OLD token is
-- enough to log the legitimate user out of every device they're
-- signed in on. Adversarial finding flagged this as a victim DoS
-- primitive (replay any historically-leaked token to nuke a user's
-- live sessions).
--
-- Fix: track which refresh row was rotated FROM which. The sweep walks
-- the descendant chain of the replayed token only, leaving other
-- chains (different devices / authorize sessions) untouched.
--
-- Schema: parent_id TEXT, nullable. NULL on initial mint (root of a
-- chain). Set to the OLD refresh row's id on each rotation. No FK to
-- self — the row points at a soon-to-be-revoked row, and adding a
-- self-FK would complicate the sweep's ordering. The chain walk uses
-- a recursive CTE (SQLite supports CTE since 3.8).
--
-- Backwards compat: rows minted before this migration have
-- parent_id=NULL. They form their own single-node chains; the sweep
-- treats them the same as a brand-new mint.

ALTER TABLE oauth_tokens ADD COLUMN parent_id TEXT;

-- No index — parent_id is read only by the recursive-CTE sweep, which
-- is per-user-per-replay (rare) and starts from a known id (PK).
