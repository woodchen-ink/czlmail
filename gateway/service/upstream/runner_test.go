package upstream_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// fake is an Upstream whose Sync results are scripted.
type fake struct {
	upstream.Upstream // unused methods panic
	idx               *mailindex.Store
	mu                sync.Mutex
	errs              []error // returned by successive Sync calls; nil entries succeed
	syncs             []upstream.SyncRequest
	watchErr          chan error
}

func (f *fake) Index() *mailindex.Store { return f.idx }

func (f *fake) Sync(_ context.Context, req upstream.SyncRequest) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs = append(f.syncs, req)
	var err error
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	if err == nil && !f.idx.Ready() {
		f.idx.Write(func(tx *mailindex.Tx) error { tx.SetReady(); return nil })
		return map[string]string{"Email": "x"}, nil
	}
	return nil, err
}

func (f *fake) Watch(ctx context.Context, hint func(upstream.Hint)) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-f.watchErr:
		return err
	}
}

type events struct {
	mu       sync.Mutex
	statuses []upstream.Status
	changed  int
}

func (e *events) StatusChanged(_ string, st upstream.Status) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.statuses = append(e.statuses, st)
}
func (e *events) Progress(string, int, int) {}
func (e *events) Changed(string, map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.changed++
}

func (e *events) states() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, s := range e.statuses {
		out = append(out, s.State)
	}
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newFake(t *testing.T, errs ...error) *fake {
	idx, err := mailindex.Open(t.TempDir(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return &fake{idx: idx, errs: errs, watchErr: make(chan error, 1)}
}

var fast = upstream.Timing{BackoffMin: 10 * time.Millisecond, BackoffMax: 40 * time.Millisecond, ThrottledMin: 20 * time.Millisecond, PollTick: time.Hour}

func run(t *testing.T, f *fake) (*events, *upstream.Runner, context.CancelFunc) {
	ev := &events{}
	r := upstream.NewRunner("acc", f, ev, slog.New(slog.NewTextHandler(io.Discard, nil)), fast)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ev, r, cancel
}

func TestRunnerFirstSyncThenOK(t *testing.T) {
	f := newFake(t)
	ev, r, _ := run(t, f)
	waitFor(t, func() bool { return r.Status().State == jmapserver.UpstreamOK })
	if got := ev.states(); len(got) != 2 || got[0] != jmapserver.UpstreamSyncing || got[1] != jmapserver.UpstreamOK {
		t.Fatalf("statuses %v", got)
	}
	// A client connecting triggers a full round.
	r.SetClients(1)
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.syncs) >= 2 && f.syncs[1].Full })
}

func TestRunnerAuthStops(t *testing.T) {
	authErr := upstream.Classify("login", &imapc.StatusError{Command: "LOGIN", Status: "NO", Code: "AUTHENTICATIONFAILED"})
	f := newFake(t, authErr)
	_, r, _ := run(t, f)
	waitFor(t, func() bool { return r.Status().State == jmapserver.UpstreamAuthRequired })
	if st := r.Status(); st.Code != jmapserver.CodeUpstreamAuthRequired {
		t.Fatalf("%+v", st)
	}
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.syncs) != 1 {
		t.Fatalf("retried after auth failure: %d syncs", len(f.syncs))
	}
}

func TestRunnerUnreachableBacksOff(t *testing.T) {
	netErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	throttled := upstream.Classify("select", &imapc.StatusError{Command: "SELECT", Status: "NO", Code: "LIMIT"})
	f := newFake(t, netErr, throttled, nil)
	ev, r, _ := run(t, f)
	waitFor(t, func() bool { return r.Status().State == jmapserver.UpstreamOK })
	got := ev.states()
	// The retry keeps the error status (no flapping back to syncing); the code
	// follows the failure: network 50210, then throttled 42910.
	want := []string{jmapserver.UpstreamSyncing, jmapserver.UpstreamUnreachable, jmapserver.UpstreamUnreachable, jmapserver.UpstreamOK}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses %v", got)
	}
	ev.mu.Lock()
	codes := []int{ev.statuses[1].Code, ev.statuses[2].Code}
	ev.mu.Unlock()
	if codes[0] != jmapserver.CodeUpstreamUnreachable || codes[1] != jmapserver.CodeUpstreamRateLimited {
		t.Fatalf("codes %v", codes)
	}

	// The watcher failing restarts the session.
	before := len(f.syncs)
	f.watchErr <- netErr
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.syncs) > before })
	waitFor(t, func() bool { return r.Status().State == jmapserver.UpstreamOK })
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind upstream.ErrorKind
	}{
		{&imapc.StatusError{Command: "LOGIN", Status: "NO", Text: "Invalid credentials"}, upstream.KindAuth},
		{&imapc.StatusError{Command: "AUTHENTICATE", Status: "NO", Code: "AUTHENTICATIONFAILED"}, upstream.KindAuth},
		{&imapc.StatusError{Command: "LOGIN", Status: "NO", Code: "UNAVAILABLE"}, upstream.KindUnreachable},
		{&imapc.StatusError{Command: "SELECT", Status: "NO", Code: "LIMIT"}, upstream.KindThrottled},
		{&imapc.StatusError{Command: "FETCH", Status: "BYE", Text: "Too many simultaneous connections"}, upstream.KindThrottled},
		{&imapc.StatusError{Command: "CREATE", Status: "NO", Code: "ALREADYEXISTS"}, upstream.KindRejected},
		{io.EOF, upstream.KindUnreachable},
	} {
		if got := upstream.Classify("x", tc.err).Kind; got != tc.kind {
			t.Errorf("%v: kind %d, want %d", tc.err, got, tc.kind)
		}
	}
}
