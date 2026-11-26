package upstream_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

type recMetrics struct {
	mu        sync.Mutex
	limited   int
	latencies []time.Duration
}

func (m *recMetrics) RateLimited(string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limited++
}

func (m *recMetrics) PushLatency(_ string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies = append(m.latencies, d)
}

func (m *recMetrics) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.limited, len(m.latencies)
}

// hinting sends one hint after the first sync; the hinted round changes Email.
type hinting struct {
	*fake
	once sync.Once
}

func (h *hinting) Watch(ctx context.Context, hint func(upstream.Hint)) error {
	h.once.Do(func() { hint(upstream.Hint{Mailbox: "INBOX"}) })
	<-ctx.Done()
	return nil
}

func (h *hinting) Sync(ctx context.Context, req upstream.SyncRequest) (map[string]string, error) {
	states, err := h.fake.Sync(ctx, req)
	if len(req.Mailboxes) > 0 {
		return map[string]string{"Email": "after-hint"}, nil
	}
	return states, err
}

func TestRunnerMetrics(t *testing.T) {
	// A throttled first attempt counts one rate-limit hit, then a hint is pushed.
	throttled := &imapc.StatusError{Command: "SELECT", Status: "NO", Code: "LIMIT"}
	up := &hinting{fake: newFake(t, throttled)}
	m := &recMetrics{}
	r := upstream.NewRunner("acc", up, &events{}, testLog(), fast)
	r.SetMetrics(m)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, func() bool { _, n := m.counts(); return n == 1 })
	if limited, _ := m.counts(); limited != 1 {
		t.Fatalf("rate-limit hits = %d", limited)
	}
	// The hint is debounced (300 ms by default) before the round starts.
	if m.latencies[0] < 300*time.Millisecond {
		t.Fatalf("latency %v shorter than the hint debounce", m.latencies[0])
	}
	if r.Status().LastSync.IsZero() {
		t.Fatal("LastSync not set after a completed round")
	}
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
