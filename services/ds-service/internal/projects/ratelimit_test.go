package projects

import (
	"testing"
	"time"
)

func TestRateLimit_PerUserCap(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Close()

	clock := time.Now()
	rl.now = func() time.Time { return clock }

	for i := 0; i < UserBucketSize; i++ {
		if !rl.Allow("user-A", "tenant-T") {
			t.Fatalf("expected allow #%d to succeed", i+1)
		}
	}
	if rl.Allow("user-A", "tenant-T") {
		t.Fatal("expected per-user cap to deny request 11")
	}
}

func TestRateLimit_PerUserRefill(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Close()

	clock := time.Now()
	rl.now = func() time.Time { return clock }

	for i := 0; i < UserBucketSize; i++ {
		_ = rl.Allow("user-B", "tenant-T")
	}
	if rl.Allow("user-B", "tenant-T") {
		t.Fatal("bucket should be exhausted")
	}

	// Advance 60s — full refill.
	clock = clock.Add(61 * time.Second)
	if !rl.Allow("user-B", "tenant-T") {
		t.Fatal("expected refill after 60s")
	}
}

func TestRateLimit_PerTenantCap(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Close()

	clock := time.Now()
	rl.now = func() time.Time { return clock }

	// Spread across many users so per-user cap doesn't fire first.
	allowed := 0
	for i := 0; i < TenantBucketSize+50; i++ {
		// rotate user every 5 requests to stay under per-user cap
		userID := "user-" + string(rune('A'+(i%40)))
		if rl.Allow(userID, "tenant-X") {
			allowed++
		}
	}
	if allowed > TenantBucketSize {
		t.Fatalf("per-tenant cap not enforced: %d > %d", allowed, TenantBucketSize)
	}
	if allowed < TenantBucketSize-1 {
		// Allow a 1-token slack for boundary races; the algorithm should
		// approve exactly TenantBucketSize at start.
		t.Fatalf("per-tenant cap too tight: %d < %d", allowed, TenantBucketSize-1)
	}
}

func TestRateLimit_DifferentTenantsIsolated(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Close()

	for i := 0; i < UserBucketSize; i++ {
		rl.Allow("alice", "tenant-1")
	}
	// User exhausted on tenant-1; with tenant-2 the user bucket is the same
	// (per-user counter), but tenant-2's bucket is fresh.
	// We expect Allow to FAIL because the per-user 10/min cap is exhausted
	// regardless of tenant.
	if rl.Allow("alice", "tenant-2") {
		t.Fatal("per-user cap is global; should deny across tenants")
	}

	// Different user on tenant-2 should succeed — fresh user bucket, fresh tenant bucket.
	if !rl.Allow("bob", "tenant-2") {
		t.Fatal("bob should still have tokens")
	}
}

func TestRateLimit_GCEvictsIdle(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Close()

	clock := time.Now()
	rl.now = func() time.Time { return clock }

	rl.Allow("idle-user", "idle-tenant")
	if _, ok := rl.users["idle-user"]; !ok {
		t.Fatal("user bucket should exist after Allow")
	}

	clock = clock.Add(rateLimitIdleTTL + time.Minute)
	rl.gcOnce()

	if _, ok := rl.users["idle-user"]; ok {
		t.Error("idle user bucket should be GC'd")
	}
	if _, ok := rl.tenants["idle-tenant"]; ok {
		t.Error("idle tenant bucket should be GC'd")
	}
}
