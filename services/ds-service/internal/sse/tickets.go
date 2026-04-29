package sse

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultTicketTTL is the canonical lifetime for a ticket: 60s, long enough
// for the plugin to receive the response and open an EventSource, short enough
// to limit the leaked-ticket attack window.
const DefaultTicketTTL = 60 * time.Second

// ticketEntry is the value stored under a ticket ID. expires is wall-clock UTC.
type ticketEntry struct {
	userID   string
	tenantID string
	traceID  string
	expires  time.Time
}

// TicketStore is the interface the SSE handler depends on. The MemoryTicketStore
// is the in-process default; Phase 7 may swap in Redis-backed storage so
// horizontally scaled instances share a ticket pool.
type TicketStore interface {
	// IssueTicket returns a fresh single-use ticket bound to (userID, tenantID,
	// traceID). The ticket expires after ttl; callers should pass DefaultTicketTTL
	// unless there's a strong reason otherwise.
	IssueTicket(userID, tenantID, traceID string, ttl time.Duration) string

	// RedeemTicket atomically deletes the ticket and returns its payload. The
	// boolean ok is false if the ticket doesn't exist, was already redeemed,
	// or has expired.
	RedeemTicket(ticketID string) (userID, tenantID, traceID string, ok bool)

	// Close stops the background GC goroutine. Safe to call multiple times.
	Close()
}

// MemoryTicketStore is the default in-process TicketStore. It keeps tickets in
// a sync.RWMutex-guarded map (mirrors the audit/persist.go mutex pattern) and
// runs a background GC tick every gcInterval to evict expired entries even if
// they're never redeemed.
type MemoryTicketStore struct {
	mu      sync.RWMutex
	tickets map[string]ticketEntry

	now    func() time.Time // injectable for tests
	stopCh chan struct{}
	stopMu sync.Mutex
	closed bool
}

// NewMemoryTicketStore returns a ready-to-use store with a 60s GC tick.
//
// gcInterval is configurable for tests; pass DefaultTicketGCInterval (60s) in
// production.
func NewMemoryTicketStore(gcInterval time.Duration) *MemoryTicketStore {
	if gcInterval <= 0 {
		gcInterval = DefaultTicketGCInterval
	}
	s := &MemoryTicketStore{
		tickets: make(map[string]ticketEntry),
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	go s.gcLoop(gcInterval)
	return s
}

// DefaultTicketGCInterval is how often the GC sweeps for expired tickets.
const DefaultTicketGCInterval = 60 * time.Second

// IssueTicket implements TicketStore.
func (s *MemoryTicketStore) IssueTicket(userID, tenantID, traceID string, ttl time.Duration) string {
	if ttl <= 0 {
		ttl = DefaultTicketTTL
	}
	id := uuid.NewString()
	s.mu.Lock()
	s.tickets[id] = ticketEntry{
		userID:   userID,
		tenantID: tenantID,
		traceID:  traceID,
		expires:  s.now().Add(ttl),
	}
	s.mu.Unlock()
	return id
}

// RedeemTicket implements TicketStore — single-use semantics: a successful
// redeem deletes the entry so the same ticket cannot be reused.
func (s *MemoryTicketStore) RedeemTicket(ticketID string) (string, string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[ticketID]
	if !ok {
		return "", "", "", false
	}
	if !s.now().Before(t.expires) {
		// Expired — delete and report failure.
		delete(s.tickets, ticketID)
		return "", "", "", false
	}
	delete(s.tickets, ticketID)
	return t.userID, t.tenantID, t.traceID, true
}

// Len returns the current count of live tickets (test helper / metrics hook).
func (s *MemoryTicketStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tickets)
}

// Close stops the GC goroutine.
func (s *MemoryTicketStore) Close() {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.stopCh)
}

func (s *MemoryTicketStore) gcLoop(interval time.Duration) {
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-tk.C:
			s.gcOnce()
		}
	}
}

// gcOnce evicts every ticket whose expiry is at or before "now". Exposed for
// tests so they can deterministically trigger a sweep.
func (s *MemoryTicketStore) gcOnce() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.tickets {
		if !now.Before(t.expires) {
			delete(s.tickets, id)
		}
	}
}
