package rules

import (
	"context"
	"testing"
)

// TestNoopPrototypeFetcher: the default fetcher returns zero links + nil error.
// This is the "no fetcher wired" path — the runner falls into the sparse
// fallback (state-coverage only).
func TestNoopPrototypeFetcher_ReturnsZeroLinks(t *testing.T) {
	f := NoopPrototypeFetcher()
	links, err := f.FetchLinks(context.Background(), "file-A", map[string]string{"node-1": "screen-1"})
	if err != nil {
		t.Fatalf("noop fetcher: unexpected error %v", err)
	}
	if links != nil && len(links) != 0 {
		t.Errorf("noop fetcher should return zero links, got %d", len(links))
	}
}

// TestNewFlowGraphRunner_NilFetcher_DefaultsToNoop ensures the constructor's
// nil-coalesce branch works — passing nil should not crash.
func TestNewFlowGraphRunner_NilFetcher_DefaultsToNoop(t *testing.T) {
	r := NewFlowGraphRunner(&stubFlowGraphLoader{}, &stubLinkStore{}, nil)
	if r == nil {
		t.Fatal("constructor returned nil")
	}
	if r.fetcher == nil {
		t.Error("nil fetcher should default to noop, not stay nil")
	}
}
