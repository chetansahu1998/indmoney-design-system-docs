// Asset-scoped signed URL tokens (Pr8 / Deferred plan D1).
//
// The PNG/KTX2 routes were authenticated via `?token=<jwt>` because image
// loaders (HTML <img>, three.js TextureLoader) cannot carry custom
// Authorization headers. Putting the full JWT in the URL leaks it to
// access logs, browser history, referrer headers, etc — anyone who reads
// the log can replay every API call until the JWT expires (7 days).
//
// This module mints and verifies short-lived (60s default) tokens scoped to
// a single (tenant_id, screen_id) pair. They cannot be used to call any
// other endpoint or to read a different screen, so log exposure is a
// 60-second window over a single asset.
//
// Token format (compact, URL-safe):
//
//	<expires_unix>.<base64url(hmac_sha256(key, "<tenant>|<screen>|<expires>"))>
//
// Both fields are concatenated with a `.` so the verifier can split before
// recomputing the MAC.

package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AssetTokenTTL is the default validity window for a freshly-minted asset
// token. Short enough that a leaked log entry is mostly harmless within a
// few minutes; long enough that a user clicking around the atlas doesn't
// keep hitting expired tokens.
const AssetTokenTTL = 60 * time.Second

// AssetTokenSigner mints + verifies HMAC-SHA256 asset tokens. Key length
// is enforced at construction time so callers can't accidentally pass a
// weak secret. Derive the key from your existing 32-byte at-rest secret
// (e.g. EncryptionKey[:]) so the rotation story stays single-track.
type AssetTokenSigner struct {
	key []byte
}

// NewAssetTokenSigner constructs the signer. Returns an error if the key
// isn't at least 32 bytes — anything shorter exposes the HMAC to brute
// force in seconds.
func NewAssetTokenSigner(key []byte) (*AssetTokenSigner, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("asset signer: key must be >= 32 bytes (got %d)", len(key))
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	return &AssetTokenSigner{key: cp}, nil
}

// Mint returns a signed token for (tenantID, screenID) valid for `ttl` from
// now. Use AssetTokenTTL as the default.
func (s *AssetTokenSigner) Mint(tenantID, screenID string, ttl time.Duration) string {
	expires := time.Now().Add(ttl).Unix()
	mac := s.mac(tenantID, screenID, expires)
	return strconv.FormatInt(expires, 10) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// Verify returns nil when the token is valid for (tenantID, screenID) and
// hasn't expired. Returns a typed error otherwise so callers can decide
// whether to log "expired" vs "tampered" differently.
func (s *AssetTokenSigner) Verify(token, tenantID, screenID string) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return ErrAssetTokenMalformed
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return ErrAssetTokenMalformed
	}
	if time.Now().Unix() > expires {
		return ErrAssetTokenExpired
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrAssetTokenMalformed
	}
	want := s.mac(tenantID, screenID, expires)
	// hmac.Equal is constant-time; do not replace with bytes.Equal.
	if !hmac.Equal(got, want) {
		return ErrAssetTokenInvalidMAC
	}
	return nil
}

func (s *AssetTokenSigner) mac(tenantID, screenID string, expires int64) []byte {
	m := hmac.New(sha256.New, s.key)
	// Field separator `|` is forbidden inside UUIDs so the canonical
	// concatenation has no parsing ambiguity.
	m.Write([]byte(tenantID))
	m.Write([]byte("|"))
	m.Write([]byte(screenID))
	m.Write([]byte("|"))
	m.Write([]byte(strconv.FormatInt(expires, 10)))
	return m.Sum(nil)
}

var (
	ErrAssetTokenMalformed  = errors.New("asset token: malformed")
	ErrAssetTokenExpired    = errors.New("asset token: expired")
	ErrAssetTokenInvalidMAC = errors.New("asset token: invalid signature")
)
