package sse

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// quietLogger discards all log output so test runs aren't flooded with
// expected drop / leak warnings.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newTestBroker(t *testing.T, opts BrokerOptions) *MemoryBroker {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = quietLogger()
	}
	b := NewMemoryBroker(opts)
	t.Cleanup(b.Close)
	return b
}

// TestSubscribePublishReceive — happy path: a subscriber sees its event in
// well under 100ms.
func TestSubscribePublishReceive(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour})

	ch, unsub, err := b.Subscribe("trace-1", "tenant-A", "user-1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	want := ProjectViewReady{ProjectSlug: "p", VersionID: "v1", Tenant: "tenant-A"}
	start := time.Now()
	b.Publish("trace-1", want)

	select {
	case got := <-ch:
		if time.Since(start) > 100*time.Millisecond {
			t.Fatalf("event took >100ms: %v", time.Since(start))
		}
		ev, ok := got.(ProjectViewReady)
		if !ok || ev != want {
			t.Fatalf("unexpected event: %#v", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive event within 500ms")
	}
}

// TestConcurrentSubscribersIsolation — 100 subscribers on 100 different trace
// IDs each receive only their own event.
func TestConcurrentSubscribersIsolation(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour, SubscriberCap: 200})

	const N = 100
	type sub struct {
		ch    <-chan Event
		unsub func()
		trace string
	}
	subs := make([]sub, N)
	for i := 0; i < N; i++ {
		traceID := fmt.Sprintf("trace-%03d", i)
		ch, unsub, err := b.Subscribe(traceID, "tenant-A", fmt.Sprintf("u-%d", i))
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		subs[i] = sub{ch: ch, unsub: unsub, trace: traceID}
	}
	defer func() {
		for _, s := range subs {
			s.unsub()
		}
	}()

	// Publish to each trace concurrently.
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(fmt.Sprintf("trace-%03d", i), ProjectViewReady{
				ProjectSlug: fmt.Sprintf("p-%d", i),
				VersionID:   fmt.Sprintf("v-%d", i),
				Tenant:      "tenant-A",
			})
		}(i)
	}
	wg.Wait()

	for i, s := range subs {
		select {
		case ev := <-s.ch:
			pvr, ok := ev.(ProjectViewReady)
			if !ok {
				t.Fatalf("sub %d got non-ProjectViewReady: %#v", i, ev)
			}
			if pvr.VersionID != fmt.Sprintf("v-%d", i) {
				t.Fatalf("sub %d got wrong version: %s", i, pvr.VersionID)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("sub %d did not receive event", i)
		}

		// No further events should be queued.
		select {
		case ev := <-s.ch:
			t.Fatalf("sub %d received unexpected extra event: %#v", i, ev)
		default:
		}
	}
}

// TestTenantFiltering — subscriber on tenant-A receives nothing when the
// publisher fires events scoped to tenant-B.
func TestTenantFiltering(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour})

	ch, unsub, err := b.Subscribe("trace-1", "tenant-A", "u-A")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	b.Publish("trace-1", ProjectViewReady{ProjectSlug: "p", VersionID: "v1", Tenant: "tenant-B"})

	select {
	case ev := <-ch:
		t.Fatalf("tenant-A subscriber unexpectedly received tenant-B event: %#v", ev)
	case <-time.After(150 * time.Millisecond):
		// expected: no delivery
	}

	// Confirm a matching-tenant publish does still land.
	b.Publish("trace-1", ProjectViewReady{ProjectSlug: "p", VersionID: "v1", Tenant: "tenant-A"})
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("matching-tenant event was not delivered")
	}
}

// TestSlowSubscriberDoesNotBlockPublisher — drops events for the slow consumer
// while a fast consumer (same trace) keeps draining without missing anything.
//
// We use a smaller channel buffer for the slow sub so we can prove the drop
// path fires, plus a publish pacing slow enough that the fast goroutine can
// keep up. The publisher must never block.
func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour, ChannelBuffer: 4})

	slowCh, slowUnsub, err := b.Subscribe("trace-1", "tenant-A", "slow")
	if err != nil {
		t.Fatalf("subscribe slow: %v", err)
	}
	defer slowUnsub()

	fastCh, fastUnsub, err := b.Subscribe("trace-1", "tenant-A", "fast")
	if err != nil {
		t.Fatalf("subscribe fast: %v", err)
	}
	defer fastUnsub()

	// Fast consumer drains aggressively in the background; signal-ready
	// before publishing so we don't lose events to a not-yet-scheduled goroutine.
	var fastReceived atomic.Int64
	fastDone := make(chan struct{})
	fastReady := make(chan struct{})
	go func() {
		defer close(fastDone)
		close(fastReady)
		for range fastCh {
			fastReceived.Add(1)
		}
	}()
	<-fastReady
	// One more yield to make sure the goroutine is parked on <-fastCh.
	runtime.Gosched()

	// Slow consumer is intentionally never drained, so its buffer fills after 4
	// events and every subsequent publish drops for it. The publisher must
	// stay non-blocking throughout.
	const totalEvents = 50
	publishStart := time.Now()
	for i := 0; i < totalEvents; i++ {
		b.Publish("trace-1", ProjectViewReady{
			ProjectSlug: "p",
			VersionID:   fmt.Sprintf("v-%d", i),
			Tenant:      "tenant-A",
		})
		// Pacing: 50/sec, ~20ms apart. Gives the fast consumer headroom and
		// proves the publisher does not block on the stalled slow one.
		time.Sleep(20 * time.Millisecond)
	}
	if elapsed := time.Since(publishStart); elapsed > 5*time.Second {
		t.Fatalf("publisher took %v — should have been non-blocking", elapsed)
	}

	// Wait briefly for fast subscriber to drain.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fastReceived.Load() >= int64(totalEvents) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := fastReceived.Load(); got != int64(totalEvents) {
		t.Fatalf("fast subscriber received %d/%d events", got, totalEvents)
	}

	// Slow consumer should have dropped events: drop counter > 0.
	if drops := b.DropCount(); drops == 0 {
		t.Fatalf("expected dropped events for slow subscriber, got 0")
	}

	// Slow consumer's channel buffer must hold no more than ChannelBuffer events.
	slowUnsub() // close slow
	count := 0
	for range slowCh {
		count++
	}
	if count > 4 {
		t.Fatalf("slow subscriber buffer held %d events; want ≤4", count)
	}

	// Drain fast consumer to clean up goroutine.
	fastUnsub()
	<-fastDone
}

// TestSubscriberCap — the cap+1 subscribe call returns ErrSubscriberCapReached.
func TestSubscriberCap(t *testing.T) {
	const cap = 8
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour, SubscriberCap: cap})

	unsubs := make([]func(), 0, cap)
	for i := 0; i < cap; i++ {
		_, unsub, err := b.Subscribe(fmt.Sprintf("trace-%d", i), "tenant-A", "u")
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		unsubs = append(unsubs, unsub)
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	if _, _, err := b.Subscribe("trace-overflow", "tenant-A", "u"); err != ErrSubscriberCapReached {
		t.Fatalf("expected ErrSubscriberCapReached, got %v", err)
	}

	// Releasing one should free a slot.
	unsubs[0]()
	unsubs = unsubs[1:]
	_, unsub, err := b.Subscribe("trace-after-free", "tenant-A", "u")
	if err != nil {
		t.Fatalf("subscribe after free: %v", err)
	}
	unsubs = append(unsubs, unsub)
}

// TestUnsubscribeIsIdempotent — calling unsubscribe twice doesn't panic and
// doesn't double-decrement the count.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour})

	_, unsub, err := b.Subscribe("trace-1", "tenant-A", "u")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	unsub()
	unsub() // must not panic

	if got := b.SubscriberCount(); got != 0 {
		t.Fatalf("subscriber count after double-unsub: %d, want 0", got)
	}
}

// TestHTTPClientDisconnectGoroutineCleanup — runs an SSE-shaped HTTP handler
// against MemoryBroker, simulates both graceful close and abrupt RST, and
// asserts goroutine count returns to baseline.
func TestHTTPClientDisconnectGoroutineCleanup(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: time.Hour})

	// Minimal SSE handler: subscribe, stream events, unsubscribe on disconnect.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch, unsub, err := b.Subscribe("trace-1", "tenant-A", "u")
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer unsub()

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: x\n\n", ev.Type())
				flusher.Flush()
			}
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	baseline := runtime.NumGoroutine()

	// --- Graceful close --------------------------------------------------
	{
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		// Wait until the server-side subscriber is registered.
		waitForSubs(t, b, 1)
		// Close gracefully.
		resp.Body.Close()
	}
	waitForSubs(t, b, 0)

	// --- Abrupt TCP close (RST) ------------------------------------------
	{
		conn, err := net.Dial("tcp", srvAddr(srv))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		// Write an HTTP/1.1 GET so the server enters the handler.
		fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", srvAddr(srv))
		// Drain the headers (best-effort) so we know the handler started.
		buf := make([]byte, 1024)
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, _ = conn.Read(buf)
		waitForSubs(t, b, 1)
		// Force RST by enabling SO_LINGER 0 on the TCP connection then close.
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
	}
	waitForSubs(t, b, 0)

	// Goroutines should return to baseline within 1s.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if delta := runtime.NumGoroutine() - baseline; delta <= 2 {
			return // within noise — net/http keeps idle conn goroutines around
		}
		time.Sleep(20 * time.Millisecond)
	}
	if delta := runtime.NumGoroutine() - baseline; delta > 4 {
		t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, runtime.NumGoroutine())
	}
}

// waitForSubs polls until SubscriberCount equals target or the deadline elapses.
func waitForSubs(t *testing.T, b *MemoryBroker, target int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.SubscriberCount() == target {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscriber count did not reach %d (have %d)", target, b.SubscriberCount())
}

func srvAddr(s *httptest.Server) string {
	// httptest.Server.URL is "http://127.0.0.1:NNNN"
	return strings.TrimPrefix(s.URL, "http://")
}

// TestHeartbeatDelivered — with a tight heartbeat the subscriber sees a
// keepalive event on its channel.
func TestHeartbeatDelivered(t *testing.T) {
	b := newTestBroker(t, BrokerOptions{Heartbeat: 50 * time.Millisecond})

	ch, unsub, err := b.Subscribe("trace-1", "tenant-A", "u")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	select {
	case ev := <-ch:
		if !IsHeartbeat(ev) {
			t.Fatalf("expected heartbeat, got %#v", ev)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no heartbeat within 500ms")
	}
}

// ─── Tickets ────────────────────────────────────────────────────────────────

// TestTicketLifecycle — issue, redeem (success), re-redeem (fail).
func TestTicketLifecycle(t *testing.T) {
	s := NewMemoryTicketStore(time.Hour)
	defer s.Close()

	id := s.IssueTicket("user-1", "tenant-A", "trace-1", DefaultTicketTTL)
	if id == "" {
		t.Fatal("empty ticket id")
	}

	uid, tid, trc, ok := s.RedeemTicket(id)
	if !ok || uid != "user-1" || tid != "tenant-A" || trc != "trace-1" {
		t.Fatalf("redeem returned %s/%s/%s ok=%v", uid, tid, trc, ok)
	}

	// Single-use: re-redeem must fail.
	if _, _, _, ok := s.RedeemTicket(id); ok {
		t.Fatal("re-redeem of single-use ticket succeeded")
	}
}

// TestTicketExpiry — a ticket older than its TTL fails redeem.
func TestTicketExpiry(t *testing.T) {
	s := NewMemoryTicketStore(time.Hour)
	defer s.Close()

	// Inject a frozen clock that we can advance.
	var nowVal atomic.Pointer[time.Time]
	t0 := time.Now()
	nowVal.Store(&t0)
	s.now = func() time.Time { return *nowVal.Load() }

	id := s.IssueTicket("u", "t", "trace", 60*time.Second)

	// Advance past TTL.
	t1 := t0.Add(61 * time.Second)
	nowVal.Store(&t1)

	if _, _, _, ok := s.RedeemTicket(id); ok {
		t.Fatal("expired ticket should not redeem")
	}
}

// TestTicketGCEvictsExpired — gcOnce removes expired entries.
func TestTicketGCEvictsExpired(t *testing.T) {
	s := NewMemoryTicketStore(time.Hour)
	defer s.Close()

	var nowVal atomic.Pointer[time.Time]
	t0 := time.Now()
	nowVal.Store(&t0)
	s.now = func() time.Time { return *nowVal.Load() }

	idLive := s.IssueTicket("u", "t", "trc", 5*time.Minute)
	idStale := s.IssueTicket("u", "t", "trc", 30*time.Second)

	t1 := t0.Add(60 * time.Second)
	nowVal.Store(&t1)

	if got := s.Len(); got != 2 {
		t.Fatalf("pre-gc Len=%d, want 2", got)
	}
	s.gcOnce()
	if got := s.Len(); got != 1 {
		t.Fatalf("post-gc Len=%d, want 1", got)
	}

	// Live ticket still redeems.
	if _, _, _, ok := s.RedeemTicket(idLive); !ok {
		t.Fatal("live ticket failed to redeem after GC")
	}
	// Stale was already evicted.
	if _, _, _, ok := s.RedeemTicket(idStale); ok {
		t.Fatal("stale ticket redeemed after GC")
	}
}

// TestTicketCloseStopsGC — calling Close twice is safe.
func TestTicketCloseIdempotent(t *testing.T) {
	s := NewMemoryTicketStore(10 * time.Millisecond)
	s.Close()
	s.Close()
}
