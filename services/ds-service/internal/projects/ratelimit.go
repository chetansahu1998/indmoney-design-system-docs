package projects

import (
	"sync"
	"time"
)

// Rate limit constants. Per-user 10/min and per-tenant 200/day are the targets
// from U4. Bucket sizes are configurable via these constants only — the helper
// is intentionally simple (token-bucket, in-memory) because Phase 1 runs as a
// single instance. Phase 7 will swap in a Redis-backed limiter.
const (
	UserBucketSize    = 10
	UserRefillSeconds = 60 // 10 tokens replenished every 60s = 10/min

	TenantBucketSize    = 200
	TenantRefillSeconds = 24 * 3600 // 200 tokens / day

	rateLimitGCInterval = 60 * time.Second
	rateLimitIdleTTL    = 10 * time.Minute // entries idle this long get swept
)

// bucket is one user's or one tenant's token reservoir.
type bucket struct {
	tokens    float64
	lastRefill time.Time
	lastUsed   time.Time
}

// RateLimiter enforces per-user and per-tenant token-bucket rate limits. Buckets
// live in-memory and idle entries are GC'd every 60s.
type RateLimiter struct {
	mu      sync.Mutex
	users   map[string]*bucket
	tenants map[string]*bucket
	now     func() time.Time

	stopCh chan struct{}
	stopMu sync.Mutex
	closed bool
}

// NewRateLimiter constructs a limiter with a background GC sweeper running.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		users:   make(map[string]*bucket),
		tenants: make(map[string]*bucket),
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	go rl.gcLoop()
	return rl
}

// Allow returns true iff there's at least one token in the user's bucket AND
// the tenant's bucket. On true, both buckets are decremented atomically.
func (rl *RateLimiter) Allow(userID, tenantID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()

	uBucket := rl.bucketFor(rl.users, userID, UserBucketSize, UserRefillSeconds, now)
	tBucket := rl.bucketFor(rl.tenants, tenantID, TenantBucketSize, TenantRefillSeconds, now)

	if uBucket.tokens < 1 || tBucket.tokens < 1 {
		// Don't decrement — both must succeed.
		return false
	}
	uBucket.tokens--
	tBucket.tokens--
	uBucket.lastUsed = now
	tBucket.lastUsed = now
	return true
}

// bucketFor lazily creates and refills a bucket. Must be called under rl.mu.
// Refill rate = bucketSize / refillSeconds tokens per second.
func (rl *RateLimiter) bucketFor(m map[string]*bucket, key string, size int, refillSec float64, now time.Time) *bucket {
	b, ok := m[key]
	if !ok {
		b = &bucket{tokens: float64(size), lastRefill: now, lastUsed: now}
		m[key] = b
		return b
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		ratePerSec := float64(size) / refillSec
		b.tokens += elapsed * ratePerSec
		if b.tokens > float64(size) {
			b.tokens = float64(size)
		}
		b.lastRefill = now
	}
	return b
}

// Close stops the GC goroutine. Safe to call multiple times.
func (rl *RateLimiter) Close() {
	rl.stopMu.Lock()
	defer rl.stopMu.Unlock()
	if rl.closed {
		return
	}
	rl.closed = true
	close(rl.stopCh)
}

func (rl *RateLimiter) gcLoop() {
	tk := time.NewTicker(rateLimitGCInterval)
	defer tk.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-tk.C:
			rl.gcOnce()
		}
	}
}

func (rl *RateLimiter) gcOnce() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	for k, b := range rl.users {
		if now.Sub(b.lastUsed) > rateLimitIdleTTL {
			delete(rl.users, k)
		}
	}
	for k, b := range rl.tenants {
		if now.Sub(b.lastUsed) > rateLimitIdleTTL {
			delete(rl.tenants, k)
		}
	}
}
