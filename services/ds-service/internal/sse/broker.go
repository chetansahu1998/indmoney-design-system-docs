package sse

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Configuration defaults. Heartbeat keeps proxies (CloudFront, nginx, Fly edge)
// from idle-closing the long-lived TCP connection. The subscriber cap is a
// blast-radius limit — a runaway client cannot exhaust file descriptors past
// it. The leak-sentinel TTL is intentionally an order of magnitude longer than
// the longest realistic project pipeline (~5 min) so genuine long subscriptions
// don't trip it; if it does fire, it's a real bug.
const (
	DefaultHeartbeat       = 15 * time.Second
	DefaultSubscriberCap   = 1024
	DefaultChannelBuffer   = 32
	DefaultLeakSentinelTTL = 1 * time.Hour
)

// ErrSubscriberCapReached is returned by Subscribe when the broker is at its
// configured cap. The HTTP layer should translate this to 503.
var ErrSubscriberCapReached = errors.New("sse: subscriber cap reached")

// BrokerService is the contract the HTTP handler depends on. Phase 7's
// RedisBroker will satisfy the same interface so callers don't change.
type BrokerService interface {
	// Subscribe registers a listener for events on traceID, scoped by tenantID
	// (cross-tenant events are filtered out). userID is recorded for logging.
	//
	// The returned channel is closed by the broker when unsubscribe runs.
	// Callers MUST invoke unsubscribe — typically `defer unsub()` immediately
	// after the call. The leak sentinel will yell via slog if it isn't called
	// within DefaultLeakSentinelTTL.
	Subscribe(traceID, tenantID, userID string) (<-chan Event, func(), error)

	// Publish fans out event to every subscriber on traceID whose tenantID
	// matches event.TenantID(). Non-blocking: events are dropped (logged) for
	// subscribers whose channel is full, so one slow client cannot stall the
	// publisher or the other subscribers.
	Publish(traceID string, event Event)

	// Close stops the heartbeat goroutine, closes every subscriber channel, and
	// fires every active leak sentinel. Safe to call multiple times.
	Close()
}

// MemoryBroker is the in-process default BrokerService. Subscribers are stored
// in a per-trace map so fan-out is O(subscribers-on-trace) rather than O(all).
type MemoryBroker struct {
	mu          sync.RWMutex
	byTrace     map[string]map[string]*subscriber // traceID → subID → sub
	totalCount  int                                // total live subscribers across traces
	heartbeat   time.Duration
	cap         int
	chanBuffer  int
	sentinelTTL time.Duration
	logger      *slog.Logger

	stopCh chan struct{}
	stopMu sync.Mutex
	closed bool

	// dropCounter is atomically incremented every time Publish drops an event
	// because a subscriber's channel was full. Exposed via DropCount() for tests
	// and metrics.
	dropCounter atomic.Int64
}

// subscriber bundles the per-listener state.
type subscriber struct {
	id       string
	traceID  string
	tenantID string
	userID   string
	ch       chan Event
	sentinel *time.Timer
}

// BrokerOptions tunes the MemoryBroker. Zero values fall back to the Default*
// constants so callers can pass MemoryBrokerOptions{} for production defaults.
type BrokerOptions struct {
	Heartbeat       time.Duration
	SubscriberCap   int
	ChannelBuffer   int
	SentinelTTL     time.Duration
	Logger          *slog.Logger
}

// NewMemoryBroker constructs a broker with the provided options, falling back
// to Default* when fields are zero. The returned broker is immediately ready;
// the heartbeat goroutine runs until Close() is called.
func NewMemoryBroker(opts BrokerOptions) *MemoryBroker {
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = DefaultHeartbeat
	}
	if opts.SubscriberCap <= 0 {
		opts.SubscriberCap = DefaultSubscriberCap
	}
	if opts.ChannelBuffer <= 0 {
		opts.ChannelBuffer = DefaultChannelBuffer
	}
	if opts.SentinelTTL <= 0 {
		opts.SentinelTTL = DefaultLeakSentinelTTL
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	b := &MemoryBroker{
		byTrace:     make(map[string]map[string]*subscriber),
		heartbeat:   opts.Heartbeat,
		cap:         opts.SubscriberCap,
		chanBuffer:  opts.ChannelBuffer,
		sentinelTTL: opts.SentinelTTL,
		logger:      opts.Logger,
		stopCh:      make(chan struct{}),
	}
	go b.heartbeatLoop()
	return b
}

// Subscribe implements BrokerService.
func (b *MemoryBroker) Subscribe(traceID, tenantID, userID string) (<-chan Event, func(), error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, errors.New("sse: broker closed")
	}
	if b.totalCount >= b.cap {
		b.mu.Unlock()
		return nil, nil, ErrSubscriberCapReached
	}

	sub := &subscriber{
		id:       uuid.NewString(),
		traceID:  traceID,
		tenantID: tenantID,
		userID:   userID,
		ch:       make(chan Event, b.chanBuffer),
	}
	subs, ok := b.byTrace[traceID]
	if !ok {
		subs = make(map[string]*subscriber)
		b.byTrace[traceID] = subs
	}
	subs[sub.id] = sub
	b.totalCount++

	// Leak sentinel: every subscribe registers a one-shot timer. If unsubscribe
	// runs in time, the timer is stopped before it fires. If it does fire, the
	// subscription has been alive for >sentinelTTL — almost certainly a leak.
	sub.sentinel = time.AfterFunc(b.sentinelTTL, func() {
		b.logger.Error("sse: subscription leak sentinel fired",
			"trace_id", traceID,
			"tenant_id", tenantID,
			"user_id", userID,
			"sub_id", sub.id,
			"ttl", b.sentinelTTL,
		)
	})
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.removeSubscriber(traceID, sub.id)
		})
	}
	return sub.ch, unsubscribe, nil
}

// removeSubscriber detaches sub, stops its sentinel, and closes its channel.
// Safe to call once per subscriber; the sync.Once in Subscribe enforces that.
func (b *MemoryBroker) removeSubscriber(traceID, subID string) {
	b.mu.Lock()
	subs, ok := b.byTrace[traceID]
	if !ok {
		b.mu.Unlock()
		return
	}
	sub, ok := subs[subID]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(subs, subID)
	if len(subs) == 0 {
		delete(b.byTrace, traceID)
	}
	b.totalCount--
	b.mu.Unlock()

	if sub.sentinel != nil {
		sub.sentinel.Stop()
	}
	close(sub.ch)
}

// Publish implements BrokerService.
func (b *MemoryBroker) Publish(traceID string, event Event) {
	if event == nil {
		return
	}
	b.mu.RLock()
	subs, ok := b.byTrace[traceID]
	if !ok {
		b.mu.RUnlock()
		return
	}
	// Snapshot the slice of channels under the read lock so we don't hold it
	// while doing the (non-blocking) sends. This keeps a slow listener from
	// stalling concurrent Subscribe / Publish calls.
	type target struct {
		sub *subscriber
	}
	targets := make([]target, 0, len(subs))
	for _, s := range subs {
		if s.tenantID != event.TenantID() {
			continue
		}
		targets = append(targets, target{sub: s})
	}
	b.mu.RUnlock()

	for _, t := range targets {
		select {
		case t.sub.ch <- event:
			// delivered
		default:
			// Drop on slow subscriber rather than block the publisher.
			b.dropCounter.Add(1)
			b.logger.Warn("sse: dropped event for slow subscriber",
				"trace_id", traceID,
				"tenant_id", t.sub.tenantID,
				"user_id", t.sub.userID,
				"event_type", event.Type(),
			)
		}
	}
}

// DropCount returns how many events have been dropped due to slow subscribers
// across the lifetime of this broker. Exposed for tests and Prometheus hooks.
func (b *MemoryBroker) DropCount() int64 {
	return b.dropCounter.Load()
}

// SubscriberCount returns the total live subscriber count (test helper).
func (b *MemoryBroker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalCount
}

// Close implements BrokerService — stops heartbeat, closes every channel,
// fires every sentinel-stop. Safe to call multiple times.
func (b *MemoryBroker) Close() {
	b.stopMu.Lock()
	if b.closed {
		b.stopMu.Unlock()
		return
	}
	b.closed = true
	close(b.stopCh)
	b.stopMu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	for traceID, subs := range b.byTrace {
		for _, s := range subs {
			if s.sentinel != nil {
				s.sentinel.Stop()
			}
			close(s.ch)
		}
		delete(b.byTrace, traceID)
	}
	b.totalCount = 0
}

// heartbeatLoop publishes a tenant-agnostic heartbeat to every subscriber on
// every trace. The heartbeat carries an empty payload — it exists purely so
// HTTP intermediaries see traffic on the connection. The HTTP handler in U4
// translates a heartbeat into an SSE comment line `: keepalive\n\n`.
func (b *MemoryBroker) heartbeatLoop() {
	tk := time.NewTicker(b.heartbeat)
	defer tk.Stop()
	for {
		select {
		case <-b.stopCh:
			return
		case <-tk.C:
			b.fireHeartbeat()
		}
	}
}

// fireHeartbeat sends a keepalive event to every subscriber, regardless of
// tenant. We bypass tenant filtering because the HTTP layer renders heartbeats
// as SSE comments rather than data events — there's no payload to leak.
func (b *MemoryBroker) fireHeartbeat() {
	b.mu.RLock()
	targets := make([]*subscriber, 0)
	for _, subs := range b.byTrace {
		for _, s := range subs {
			targets = append(targets, s)
		}
	}
	b.mu.RUnlock()

	hb := heartbeatEvent{}
	for _, s := range targets {
		select {
		case s.ch <- hb:
		default:
			// If even the heartbeat can't squeeze in, the subscriber is
			// thoroughly stuck. Don't bump the drop counter — heartbeats are
			// best-effort and should not be alert-worthy.
		}
	}
}

// heartbeatEvent is an internal sentinel value the HTTP handler recognizes via
// its Type() == "heartbeat" and translates into an SSE comment line. It carries
// no payload.
type heartbeatEvent struct{}

// TenantID implements Event.
func (heartbeatEvent) TenantID() string { return "" }

// Type implements Event.
func (heartbeatEvent) Type() string { return "heartbeat" }

// Payload implements Event.
func (heartbeatEvent) Payload() any { return nil }

// IsHeartbeat reports whether the event is a broker-emitted keepalive ping.
// The HTTP handler uses this to render `: keepalive\n\n` instead of `data: ...`.
func IsHeartbeat(e Event) bool {
	_, ok := e.(heartbeatEvent)
	return ok
}
