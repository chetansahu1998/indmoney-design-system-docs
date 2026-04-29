package projects

import (
	"bytes"
	"testing"
	"time"
)

func TestIdempotency_StoreAndCheck(t *testing.T) {
	c := NewIdempotencyCache()
	defer c.Close()

	body := []byte(`{"version_id":"v1"}`)
	c.Store("key-A", body)

	got, hit := c.Check("key-A")
	if !hit {
		t.Fatal("expected cache hit")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %s want %s", got, body)
	}
}

func TestIdempotency_MissAfterTTL(t *testing.T) {
	c := NewIdempotencyCache()
	defer c.Close()

	clock := time.Now()
	c.now = func() time.Time { return clock }

	c.StoreWithTTL("key-B", []byte("x"), 100*time.Millisecond)
	if _, hit := c.Check("key-B"); !hit {
		t.Fatal("expected initial hit")
	}

	clock = clock.Add(200 * time.Millisecond)
	if _, hit := c.Check("key-B"); hit {
		t.Fatal("expected miss after TTL")
	}
}

func TestIdempotency_EmptyKeyIgnored(t *testing.T) {
	c := NewIdempotencyCache()
	defer c.Close()

	c.Store("", []byte("ignored"))
	if _, hit := c.Check(""); hit {
		t.Fatal("expected miss on empty key")
	}
}

func TestIdempotency_GCEvictsExpired(t *testing.T) {
	c := NewIdempotencyCache()
	defer c.Close()

	clock := time.Now()
	c.now = func() time.Time { return clock }

	c.StoreWithTTL("key-C", []byte("x"), 50*time.Millisecond)
	c.StoreWithTTL("key-D", []byte("y"), 1*time.Hour)

	clock = clock.Add(100 * time.Millisecond)
	c.gcOnce()

	c.mu.RLock()
	_, hasC := c.entries["key-C"]
	_, hasD := c.entries["key-D"]
	c.mu.RUnlock()

	if hasC {
		t.Error("expected expired key-C to be GC'd")
	}
	if !hasD {
		t.Error("expected key-D to survive GC")
	}
}

func TestIdempotency_ReplayWithinTTLReturnsCached(t *testing.T) {
	c := NewIdempotencyCache()
	defer c.Close()

	first := []byte(`{"first":"response"}`)
	c.Store("idem-1", first)

	// Concurrent retry within TTL gets identical bytes.
	got, hit := c.Check("idem-1")
	if !hit || !bytes.Equal(got, first) {
		t.Fatalf("replay didn't return cached response")
	}
}
