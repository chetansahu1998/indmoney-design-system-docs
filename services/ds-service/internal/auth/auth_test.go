package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// ─── Password hashing ────────────────────────────────────────────────────────

func TestHashPassword_RejectsShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error for password < 8 chars")
	}
}

func TestHashPassword_RoundTrip(t *testing.T) {
	pw := "correct-horse-battery"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == pw {
		t.Fatal("hash equals plaintext — bcrypt not applied")
	}
	if err := VerifyPassword(hash, pw); err != nil {
		t.Fatalf("verify good password: %v", err)
	}
	if err := VerifyPassword(hash, "wrong"); err == nil {
		t.Fatal("verify wrong password must fail")
	}
}

func TestVerifyPassword_RejectsMalformedHash(t *testing.T) {
	if err := VerifyPassword("not-a-bcrypt-hash", "password123"); err == nil {
		t.Fatal("malformed hash must fail verification")
	}
}

// ─── Role helpers ────────────────────────────────────────────────────────────

func TestCanSync(t *testing.T) {
	allowed := []string{RoleTenantAdmin, RoleDesigner, RoleEngineer}
	denied := []string{RoleSuperAdmin, RoleViewer, "", "unknown_role"}
	for _, r := range allowed {
		if !CanSync(r) {
			t.Errorf("CanSync(%q) = false, want true", r)
		}
	}
	for _, r := range denied {
		if CanSync(r) {
			t.Errorf("CanSync(%q) = true, want false", r)
		}
	}
}

func TestCanAudit(t *testing.T) {
	if !CanAudit(RoleTenantAdmin) {
		t.Error("tenant_admin should be able to audit")
	}
	for _, r := range []string{RoleDesigner, RoleEngineer, RoleViewer, RoleSuperAdmin, ""} {
		if CanAudit(r) {
			t.Errorf("CanAudit(%q) = true, want false", r)
		}
	}
}

// ─── JWT signing key ─────────────────────────────────────────────────────────

func TestGenerateSigningKey(t *testing.T) {
	k, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(k.Priv) == 0 || len(k.Pub) == 0 {
		t.Fatal("generated key has empty priv or pub")
	}
}

func TestEncodeDecodeSigningKey_RoundTrip(t *testing.T) {
	k, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	priv := k.EncodePriv()
	pub := k.EncodePub()
	loaded, err := LoadSigningKey(priv, pub)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(loaded.Priv, k.Priv) || !bytes.Equal(loaded.Pub, k.Pub) {
		t.Fatal("round-tripped key bytes differ from original")
	}
}

func TestLoadSigningKey_DerivesPubFromPriv(t *testing.T) {
	k, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	loaded, err := LoadSigningKey(k.EncodePriv(), "")
	if err != nil {
		t.Fatalf("load with empty pub: %v", err)
	}
	if !bytes.Equal(loaded.Pub, k.Pub) {
		t.Fatal("public key derived from private differs from original public")
	}
}

func TestLoadSigningKey_RejectsBadBase64(t *testing.T) {
	if _, err := LoadSigningKey("not-base64!@#", ""); err == nil {
		t.Fatal("expected error on bad base64")
	}
}

func TestLoadSigningKey_RejectsWrongSize(t *testing.T) {
	// 32 bytes is wrong for ed25519 private (needs 64).
	if _, err := LoadSigningKey("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", ""); err == nil {
		t.Fatal("expected error on wrong key size")
	}
}

// ─── JWT mint + verify ───────────────────────────────────────────────────────

func TestMintAccessToken_VerifyRoundTrip(t *testing.T) {
	k, _ := GenerateSigningKey()
	userID := "u-1"
	tenants := []string{"t-1", "t-2"}
	tok, err := k.MintAccessToken(userID, "u@ex.com", RoleSuperAdmin, tenants, 5*time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.Contains(tok, ".") {
		t.Fatal("minted token doesn't look like a JWT")
	}
	claims, err := k.VerifyAccessToken(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Sub != userID {
		t.Errorf("Sub = %q, want %q", claims.Sub, userID)
	}
	if !claims.IsAdmin {
		t.Error("IsAdmin should be true for super_admin")
	}
	if len(claims.Tenants) != 2 || claims.Tenants[0] != "t-1" || claims.Tenants[1] != "t-2" {
		t.Errorf("tenants = %v, want [t-1 t-2]", claims.Tenants)
	}
	if claims.ID == "" {
		t.Error("jti (claims.ID) should be set")
	}
}

func TestMintAccessToken_NonSuperAdminNotIsAdmin(t *testing.T) {
	k, _ := GenerateSigningKey()
	tok, _ := k.MintAccessToken("u", "u@ex.com", RoleDesigner, []string{"t"}, time.Minute)
	claims, err := k.VerifyAccessToken(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.IsAdmin {
		t.Error("designer role must not set IsAdmin")
	}
}

func TestVerifyAccessToken_RejectsForeignKey(t *testing.T) {
	k1, _ := GenerateSigningKey()
	k2, _ := GenerateSigningKey()
	tok, _ := k1.MintAccessToken("u", "u@ex.com", RoleViewer, []string{"t"}, time.Minute)
	if _, err := k2.VerifyAccessToken(tok); err == nil {
		t.Fatal("token signed by k1 must NOT verify under k2")
	}
}

func TestVerifyAccessToken_RejectsExpired(t *testing.T) {
	k, _ := GenerateSigningKey()
	tok, _ := k.MintAccessToken("u", "u@ex.com", RoleViewer, []string{"t"}, -1*time.Second)
	if _, err := k.VerifyAccessToken(tok); err == nil {
		t.Fatal("expired token must not verify")
	}
}

func TestVerifyAccessToken_RejectsTampered(t *testing.T) {
	k, _ := GenerateSigningKey()
	tok, _ := k.MintAccessToken("u", "u@ex.com", RoleViewer, []string{"t"}, time.Minute)
	// Flip a byte in the payload segment.
	tampered := tok[:len(tok)/2] + "x" + tok[len(tok)/2+1:]
	if _, err := k.VerifyAccessToken(tampered); err == nil {
		t.Fatal("tampered token must not verify")
	}
}

func TestVerifyAccessToken_RejectsGarbage(t *testing.T) {
	k, _ := GenerateSigningKey()
	if _, err := k.VerifyAccessToken("not.a.jwt"); err == nil {
		t.Fatal("garbage must not verify")
	}
	if _, err := k.VerifyAccessToken(""); err == nil {
		t.Fatal("empty string must not verify")
	}
}

// ─── AES-GCM encryption (Figma PAT at-rest) ──────────────────────────────────

func TestGenerateEncryptionKey_LoadRoundTrip(t *testing.T) {
	b64, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	k, err := LoadEncryptionKey(b64)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if k == nil {
		t.Fatal("loaded key is nil")
	}
}

func TestLoadEncryptionKey_RejectsWrongSize(t *testing.T) {
	// 16 bytes (AES-128) — not accepted for AES-256.
	if _, err := LoadEncryptionKey("AAAAAAAAAAAAAAAAAAAAAA=="); err == nil {
		t.Fatal("expected error on 16-byte key")
	}
}

func TestLoadEncryptionKey_RejectsBadBase64(t *testing.T) {
	if _, err := LoadEncryptionKey("not base64!"); err == nil {
		t.Fatal("expected error on bad base64")
	}
}

func TestEncrypt_Decrypt_RoundTrip(t *testing.T) {
	b64, _ := GenerateEncryptionKey()
	k, _ := LoadEncryptionKey(b64)
	plain := []byte("figma_pat_secret_12345")
	sealed, err := k.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(sealed, plain) {
		t.Fatal("sealed bytes equal plaintext — encryption did not run")
	}
	got, err := k.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("decrypted = %q, want %q", got, plain)
	}
}

func TestEncrypt_NonceUniqueness(t *testing.T) {
	// Encrypting the same plaintext twice MUST produce different ciphertexts
	// (random nonce). A constant nonce is a textbook GCM vulnerability.
	b64, _ := GenerateEncryptionKey()
	k, _ := LoadEncryptionKey(b64)
	plain := []byte("repeat-me")
	a, _ := k.Encrypt(plain)
	b, _ := k.Encrypt(plain)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext produced identical ciphertext — nonce reuse")
	}
}

func TestDecrypt_RejectsTampered(t *testing.T) {
	b64, _ := GenerateEncryptionKey()
	k, _ := LoadEncryptionKey(b64)
	sealed, _ := k.Encrypt([]byte("authentic"))
	// Flip a bit in the tag (last 16 bytes).
	sealed[len(sealed)-1] ^= 0x01
	if _, err := k.Decrypt(sealed); err == nil {
		t.Fatal("tampered ciphertext must not decrypt")
	}
}

func TestDecrypt_RejectsForeignKey(t *testing.T) {
	a64, _ := GenerateEncryptionKey()
	b64, _ := GenerateEncryptionKey()
	kA, _ := LoadEncryptionKey(a64)
	kB, _ := LoadEncryptionKey(b64)
	sealed, _ := kA.Encrypt([]byte("secret"))
	if _, err := kB.Decrypt(sealed); err == nil {
		t.Fatal("ciphertext from key A must not decrypt under key B")
	}
}

func TestDecrypt_RejectsShort(t *testing.T) {
	b64, _ := GenerateEncryptionKey()
	k, _ := LoadEncryptionKey(b64)
	if _, err := k.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("ciphertext shorter than nonce must error")
	}
}
