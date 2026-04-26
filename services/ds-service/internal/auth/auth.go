// Package auth provides JWT minting + verification, password hashing,
// and AES-GCM encryption for at-rest secrets (Figma PATs).
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Roles
const (
	RoleSuperAdmin  = "super_admin"
	RoleTenantAdmin = "tenant_admin"
	RoleDesigner    = "designer"
	RoleEngineer    = "engineer"
	RoleViewer      = "viewer"
)

// CanSync reports whether a tenant role grants sync permission.
func CanSync(role string) bool {
	switch role {
	case RoleTenantAdmin, RoleDesigner, RoleEngineer:
		return true
	}
	return false
}

// CanAudit reports whether a tenant role grants audit-log read permission.
func CanAudit(role string) bool {
	switch role {
	case RoleTenantAdmin:
		return true
	}
	return false
}

// ─── Password hashing ────────────────────────────────────────────────────────

// HashPassword creates a bcrypt hash with cost 12 (OWASP-recommended for v1).
func HashPassword(plain string) (string, error) {
	if len(plain) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// VerifyPassword constant-time compares plain against the stored hash.
func VerifyPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// ─── JWT signing key handling ────────────────────────────────────────────────

// SigningKey wraps an Ed25519 private key for JWT signing.
type SigningKey struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// GenerateSigningKey creates a new Ed25519 keypair.
func GenerateSigningKey() (*SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SigningKey{Priv: priv, Pub: pub}, nil
}

// LoadSigningKey decodes a base64-encoded private key (as written by genkey.go).
func LoadSigningKey(privB64, pubB64 string) (*SigningKey, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return nil, fmt.Errorf("decode priv: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("priv key wrong size: got %d expected %d", len(privBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(privBytes)

	var pub ed25519.PublicKey
	if pubB64 != "" {
		pubBytes, err := base64.StdEncoding.DecodeString(pubB64)
		if err != nil {
			return nil, fmt.Errorf("decode pub: %w", err)
		}
		pub = ed25519.PublicKey(pubBytes)
	} else {
		pub = priv.Public().(ed25519.PublicKey)
	}
	return &SigningKey{Priv: priv, Pub: pub}, nil
}

// EncodePub returns the base64-encoded public key (for sharing with /api/sync proxy).
func (k *SigningKey) EncodePub() string {
	return base64.StdEncoding.EncodeToString(k.Pub)
}

// EncodePriv returns the base64-encoded private key (write to Fly secret / .env.local).
func (k *SigningKey) EncodePriv() string {
	return base64.StdEncoding.EncodeToString(k.Priv)
}

// ─── JWT claims ──────────────────────────────────────────────────────────────

// Claims is what the access token carries. Following RFC 9068 access-token semantics.
type Claims struct {
	jwt.RegisteredClaims

	Sub      string   `json:"sub"`
	Email    string   `json:"email"`
	Role     string   `json:"role"` // user-level role: super_admin | user
	Tenants  []string `json:"tenants"`
	IsAdmin  bool     `json:"is_admin"`
}

// MintAccessToken creates a 7-day access JWT for a user.
func (k *SigningKey) MintAccessToken(userID, email, role string, tenants []string, lifetime time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "indmoney-ds-service",
			Subject:   userID,
			Audience:  []string{"ds-service"},
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
		Sub:     userID,
		Email:   email,
		Role:    role,
		Tenants: tenants,
		IsAdmin: role == RoleSuperAdmin,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return tok.SignedString(k.Priv)
}

// VerifyAccessToken parses + validates a JWT and returns the claims.
func (k *SigningKey) VerifyAccessToken(raw string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return k.Pub, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// ─── AES-GCM at-rest encryption (for Figma PATs) ─────────────────────────────

// EncryptionKey wraps a 32-byte AES-256 key.
type EncryptionKey [32]byte

// LoadEncryptionKey decodes a base64-encoded 32-byte key.
func LoadEncryptionKey(b64 string) (*EncryptionKey, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(b))
	}
	var k EncryptionKey
	copy(k[:], b)
	return &k, nil
}

// GenerateEncryptionKey returns a random AES-256 key, base64-encoded.
func GenerateEncryptionKey() (string, error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// Encrypt seals plaintext with AES-256-GCM. Output: nonce(12) || ciphertext || tag(16).
func (k *EncryptionKey) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return out, nil
}

// Decrypt opens a sealed blob produced by Encrypt.
func (k *EncryptionKey) Decrypt(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("sealed too short")
	}
	nonce := sealed[:gcm.NonceSize()]
	ct := sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
