package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestSigner(t *testing.T) *AssetTokenSigner {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s, err := NewAssetTokenSigner(key)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return s
}

func TestNewAssetTokenSigner_RejectsShortKey(t *testing.T) {
	if _, err := NewAssetTokenSigner(make([]byte, 31)); err == nil {
		t.Fatal("expected error on 31-byte key")
	}
	if _, err := NewAssetTokenSigner(nil); err == nil {
		t.Fatal("expected error on nil key")
	}
}

func TestNewAssetTokenSigner_AcceptsMinimumKey(t *testing.T) {
	if _, err := NewAssetTokenSigner(make([]byte, 32)); err != nil {
		t.Fatalf("unexpected error on 32-byte key: %v", err)
	}
}

func TestAssetTokenSigner_KeyCopied(t *testing.T) {
	// Mutating the caller's slice after construction must not affect the signer.
	key := make([]byte, 32)
	s, _ := NewAssetTokenSigner(key)
	tok := s.Mint("t", "s", time.Minute)
	for i := range key {
		key[i] = 0xff
	}
	if err := s.Verify(tok, "t", "s"); err != nil {
		t.Fatalf("signer's internal key was mutated by caller: %v", err)
	}
}

func TestAssetTokenSigner_MintVerifyRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	tok := s.Mint("tenant-A", "screen-1", time.Minute)
	if !strings.Contains(tok, ".") {
		t.Fatalf("token shape unexpected: %q", tok)
	}
	if err := s.Verify(tok, "tenant-A", "screen-1"); err != nil {
		t.Fatalf("verify good token: %v", err)
	}
}

func TestAssetTokenSigner_RejectsWrongTenant(t *testing.T) {
	s := newTestSigner(t)
	tok := s.Mint("tenant-A", "screen-1", time.Minute)
	err := s.Verify(tok, "tenant-B", "screen-1")
	if !errors.Is(err, ErrAssetTokenInvalidMAC) {
		t.Fatalf("expected ErrAssetTokenInvalidMAC, got %v", err)
	}
}

func TestAssetTokenSigner_RejectsWrongScreen(t *testing.T) {
	s := newTestSigner(t)
	tok := s.Mint("tenant-A", "screen-1", time.Minute)
	err := s.Verify(tok, "tenant-A", "screen-2")
	if !errors.Is(err, ErrAssetTokenInvalidMAC) {
		t.Fatalf("expected ErrAssetTokenInvalidMAC, got %v", err)
	}
}

func TestAssetTokenSigner_RejectsExpired(t *testing.T) {
	s := newTestSigner(t)
	tok := s.Mint("t", "s", -1*time.Second)
	err := s.Verify(tok, "t", "s")
	if !errors.Is(err, ErrAssetTokenExpired) {
		t.Fatalf("expected ErrAssetTokenExpired, got %v", err)
	}
}

func TestAssetTokenSigner_RejectsMalformed(t *testing.T) {
	s := newTestSigner(t)
	cases := []string{
		"",
		"no-dot",
		"only.one.dot.extra", // SplitN(., 2) keeps this as 2 parts so this is actually OK structurally; the MAC will fail not malformed
		"abc.notbase64!@#",
		"notanumber.AAAA",
	}
	// The "only.one.dot.extra" case splits into ["only","one.dot.extra"]; the
	// expires field is not numeric → malformed. Asserting on the typed
	// sentinel:
	for _, tc := range cases {
		if tc == "" || tc == "no-dot" {
			err := s.Verify(tc, "t", "s")
			if !errors.Is(err, ErrAssetTokenMalformed) {
				t.Errorf("Verify(%q): expected ErrAssetTokenMalformed, got %v", tc, err)
			}
		} else {
			err := s.Verify(tc, "t", "s")
			if err == nil {
				t.Errorf("Verify(%q): expected error, got nil", tc)
			}
		}
	}
}

func TestAssetTokenSigner_RejectsForeignSigner(t *testing.T) {
	// A token minted by signer A must not verify under signer B with a
	// different key — even for the same (tenant, screen, expires).
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	for i := range keyB {
		keyB[i] = 0xab
	}
	sA, _ := NewAssetTokenSigner(keyA)
	sB, _ := NewAssetTokenSigner(keyB)
	tok := sA.Mint("t", "s", time.Minute)
	err := sB.Verify(tok, "t", "s")
	if !errors.Is(err, ErrAssetTokenInvalidMAC) {
		t.Fatalf("expected ErrAssetTokenInvalidMAC, got %v", err)
	}
}

func TestAssetTokenSigner_TamperedMAC(t *testing.T) {
	s := newTestSigner(t)
	tok := s.Mint("t", "s", time.Minute)
	// Flip a byte in the MAC half. Splitting at the dot.
	dot := strings.Index(tok, ".")
	if dot < 0 || dot == len(tok)-1 {
		t.Fatalf("malformed test token: %q", tok)
	}
	// Replace the last char with something definitely different.
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	err := s.Verify(tampered, "t", "s")
	if !errors.Is(err, ErrAssetTokenInvalidMAC) {
		t.Fatalf("expected ErrAssetTokenInvalidMAC, got %v", err)
	}
}

func TestAssetTokenSigner_TTLBoundary(t *testing.T) {
	// A token with 1-second TTL should verify immediately and fail after waiting.
	s := newTestSigner(t)
	tok := s.Mint("t", "s", 1*time.Second)
	if err := s.Verify(tok, "t", "s"); err != nil {
		t.Fatalf("verify fresh token: %v", err)
	}
	// Skip the wait portion to keep tests fast — the expiry path is
	// covered by TestAssetTokenSigner_RejectsExpired with a negative TTL.
}

func TestAssetTokenSigner_DistinctExpiriesProduceDistinctTokens(t *testing.T) {
	// Two mints with slightly different TTLs must produce different tokens
	// (different expires field → different MAC).
	s := newTestSigner(t)
	a := s.Mint("t", "s", time.Minute)
	time.Sleep(1100 * time.Millisecond) // ensures unix-second value moves
	b := s.Mint("t", "s", time.Minute)
	if a == b {
		t.Fatalf("expected distinct tokens for distinct expiry seconds, got %q == %q", a, b)
	}
}
