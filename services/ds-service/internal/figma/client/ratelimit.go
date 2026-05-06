package client

import (
	"context"
	"sync"
	"time"
)

// Figma's documented per-PAT rate limits on the Professional plan, by
// endpoint tier. Source: https://www.figma.com/developers/api#rate-limits
//
// Tier-1 (GET file, GET file nodes, GET image):              15 req/min
// Tier-2 (GET image fills, variables, comments, projects):   50 req/min
// Tier-3 (components, styles, file metadata, payments):     100 req/min
//
// We pace at 80% of the documented cap to leave headroom for clock skew,
// network jitter, and the slight burst that happens when a refill aligns
// with a request. Burning all 100% of the budget routinely tripped 429s
// during the sheets-sync rebuild, with each retry costing the server's
// Retry-After (typically 30s) and 3 attempts hard-failing the pipeline.
const (
	tier1RPM = 12 // 80% of 15
	tier2RPM = 40 // 80% of 50
	tier3RPM = 80 // 80% of 100

	rateLimitWindow = time.Minute
)

// rateTier identifies which token bucket a request consumes.
type rateTier int

const (
	tier1 rateTier = iota + 1
	tier2
	tier3
)

// tokenBucket is a classic leaky-token-bucket rate limiter. Capacity caps
// burst size; tokens refill at refillPerSecond regardless of consumption.
// Wait blocks until a token is available or ctx is cancelled.
//
// Concurrency: a single mutex protects all state. Wait holds the lock
// only across token math; the time.After sleep happens unlocked so other
// goroutines can claim tokens that arrive sooner. Worth-it because under
// contention every Figma call would otherwise serialise behind one wait.
type tokenBucket struct {
	mu               sync.Mutex
	capacity         float64
	tokens           float64
	refillPerSecond  float64
	last             time.Time
	now              func() time.Time // injectable for tests
}

func newTokenBucketRPM(rpm int) *tokenBucket {
	return &tokenBucket{
		capacity:        float64(rpm),
		tokens:          float64(rpm), // start full
		refillPerSecond: float64(rpm) / rateLimitWindow.Seconds(),
		last:            time.Now(),
		now:             time.Now,
	}
}

// Wait blocks until at least one token is available, then consumes it.
// Returns ctx.Err() if cancelled while waiting. Safe for concurrent use.
func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := b.now()
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * b.refillPerSecond
			if b.tokens > b.capacity {
				b.tokens = b.capacity
			}
			b.last = now
		}
		if b.tokens >= 1 {
			b.tokens -= 1
			b.mu.Unlock()
			return nil
		}
		// Compute how long until the next whole token arrives. b.tokens
		// is in [0,1); deficit = 1 - tokens; wait = deficit / rate.
		deficit := 1 - b.tokens
		wait := time.Duration(deficit/b.refillPerSecond*float64(time.Second)) + time.Millisecond
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// limiters bundles the three buckets so Client carries one struct, not
// three separate fields, and tests can swap a no-op variant.
type limiters struct {
	tier1, tier2, tier3 *tokenBucket
}

func newLimiters() *limiters {
	return &limiters{
		tier1: newTokenBucketRPM(tier1RPM),
		tier2: newTokenBucketRPM(tier2RPM),
		tier3: newTokenBucketRPM(tier3RPM),
	}
}

func (l *limiters) wait(ctx context.Context, tier rateTier) error {
	switch tier {
	case tier1:
		return l.tier1.Wait(ctx)
	case tier2:
		return l.tier2.Wait(ctx)
	case tier3:
		return l.tier3.Wait(ctx)
	}
	return nil
}
