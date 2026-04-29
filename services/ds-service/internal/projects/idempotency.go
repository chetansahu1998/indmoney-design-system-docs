package projects

import (
	"sync"
	"time"
)

// IdempotencyTTL is how long a key + cached response stays addressable. 60s
// matches the plugin's submit-button debounce + the leaked-ticket attack
// window for SSE — a designer's accidental double-submit lands within this
// window.
const IdempotencyTTL = 60 * time.Second

const idempotencyGCInterval = 60 * time.Second

// idempotencyEntry is one cached request → response mapping.
type idempotencyEntry struct {
	response []byte
	expires  time.Time
}

// IdempotencyCache stores recent (idempotency_key → response) tuples so a
// concurrent retry within IdempotencyTTL gets the same response back instead
// of triggering a second pipeline run. In-memory map; sync.RWMutex.
type IdempotencyCache struct {
	mu      sync.RWMutex
	entries map[string]idempotencyEntry
	now     func() time.Time

	stopCh chan struct{}
	stopMu sync.Mutex
	closed bool
}

// NewIdempotencyCache returns a ready-to-use cache with a background GC tick.
func NewIdempotencyCache() *IdempotencyCache {
	c := &IdempotencyCache{
		entries: make(map[string]idempotencyEntry),
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	go c.gcLoop()
	return c
}

// Check returns the cached response (if any) for key. The boolean hit reports
// whether the entry was present AND unexpired. On expiry, the entry is evicted
// and Check reports a miss.
func (c *IdempotencyCache) Check(key string) ([]byte, bool) {
	if key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !c.now().Before(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.response, true
}

// Store records a (key, response) tuple with the package-default TTL. Caller
// must pass the marshaled response bytes — typically the JSON body of the
// initial 202 reply.
func (c *IdempotencyCache) Store(key string, response []byte) {
	c.StoreWithTTL(key, response, IdempotencyTTL)
}

// StoreWithTTL is the explicit-TTL variant exposed for tests.
func (c *IdempotencyCache) StoreWithTTL(key string, response []byte, ttl time.Duration) {
	if key == "" || ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.entries[key] = idempotencyEntry{response: response, expires: c.now().Add(ttl)}
	c.mu.Unlock()
}

// Close stops the GC goroutine. Safe to call multiple times.
func (c *IdempotencyCache) Close() {
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.stopCh)
}

func (c *IdempotencyCache) gcLoop() {
	tk := time.NewTicker(idempotencyGCInterval)
	defer tk.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-tk.C:
			c.gcOnce()
		}
	}
}

func (c *IdempotencyCache) gcOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
}
