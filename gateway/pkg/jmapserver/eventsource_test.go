package jmapserver_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core/push"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// stream connects a go-jmap EventSource as alice, the way czlmail's syncer does.
type stream struct {
	es      *push.EventSource
	changes chan *jmap.StateChange
	ended   chan error
	close   context.CancelFunc
}

// ctxTransport cancels every request with ctx; go-jmap's EventSource.Close races
// with its own connect, so streams are closed this way (as czlmail does).
type ctxTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t ctxTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(r.WithContext(t.ctx))
}

func (e *env) stream(events []jmap.EventType, closeAfter bool) *stream {
	e.t.Helper()
	c := e.client("alice")
	if err := c.Authenticate(); err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hc := *c.HttpClient
	hc.Transport = ctxTransport{ctx: ctx, base: hc.Transport}
	c.HttpClient = &hc
	s := &stream{changes: make(chan *jmap.StateChange, 8), ended: make(chan error, 1), close: cancel}
	s.es = &push.EventSource{Client: c, Events: events, CloseAfterState: closeAfter,
		Handler: func(sc *jmap.StateChange) { s.changes <- sc }}
	go func() { s.ended <- s.es.Listen() }()
	e.t.Cleanup(cancel)
	// Wait until the server has registered the connection.
	waitFor(e.t, func() bool { return jmapserver.Subscribers(e.srv, "usr_alice") > 0 })
	return s
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *stream) next(t *testing.T, within time.Duration) *jmap.StateChange {
	t.Helper()
	select {
	case sc := <-s.changes:
		return sc
	case <-time.After(within):
		t.Fatal("no StateChange")
		return nil
	}
}

func (s *stream) none(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case sc := <-s.changes:
		t.Fatalf("unexpected StateChange %+v", sc.Changed)
	case <-time.After(within):
	}
}

func TestEventSourceDebounceMergesAccounts(t *testing.T) {
	e := newEnv(t, jmapserver.DefaultPushDebounce)
	s := e.stream(nil, false)
	bus := e.srv.Changes()
	start := time.Now()
	bus.Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "s1", "Mailbox": "m1"}})
	time.Sleep(100 * time.Millisecond)
	bus.Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "s2"}, "a2": {"Email": "g1"}})
	bus.Publish("usr_bob", jmapserver.Changes{"b1": {"Email": "x"}}) // another user

	sc := s.next(t, 2*time.Second)
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("sent after %v, before the debounce window", elapsed)
	}
	if sc.Type != "StateChange" || len(sc.Changed) != 2 ||
		sc.Changed["a1"]["Email"] != "s2" || sc.Changed["a1"]["Mailbox"] != "m1" || sc.Changed["a2"]["Email"] != "g1" {
		t.Fatalf("merged change: %+v", sc.Changed)
	}
	s.none(t, 700*time.Millisecond)
}

func TestEventSourceTypesFilter(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	s := e.stream([]jmap.EventType{"Email", "Thread"}, false)
	bus := e.srv.Changes()
	bus.Publish("usr_alice", jmapserver.Changes{"a1": {"Mailbox": "m1"}})
	s.none(t, 200*time.Millisecond)
	bus.Publish("usr_alice", jmapserver.Changes{"a1": {"Mailbox": "m2", "Email": "e1"}})
	sc := s.next(t, time.Second)
	if len(sc.Changed["a1"]) != 1 || sc.Changed["a1"]["Email"] != "e1" {
		t.Fatalf("filtered change: %+v", sc.Changed)
	}
}

func TestEventSourceCloseAfterState(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	s := e.stream(nil, true)
	e.srv.Changes().Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "e1"}})
	s.next(t, time.Second)
	select {
	case <-s.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("stream not closed after the state event")
	}
}

func TestEventSourceEndUserAndDisconnect(t *testing.T) {
	e := newEnv(t, 20*time.Millisecond)
	s := e.stream(nil, false)
	if n := e.srv.EndUserStreams("usr_alice"); n != 1 {
		t.Fatalf("ended %d streams", n)
	}
	select {
	case <-s.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("stream still open after EndUserStreams")
	}

	// A client that goes away is unsubscribed.
	s = e.stream(nil, false)
	s.close()
	waitFor(t, func() bool { return jmapserver.Subscribers(e.srv, "usr_alice") == 0 })
}

func TestEventSourcePing(t *testing.T) {
	e := newEnv(t, 0)
	req, _ := http.NewRequest("GET", e.ts.URL+"/jmap/eventsource?types=*&closeafter=no&ping=1", nil)
	req.SetBasicAuth("alice", "pwalice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	sc := bufio.NewScanner(resp.Body)
	var lines []string
	for len(lines) < 2 && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if strings.Join(lines, "\n") != "event: ping\ndata: {\"interval\":1}" {
		t.Fatalf("ping: %q", lines)
	}

	if status, _, _ := e.get("/jmap/eventsource?ping=-1", "alice"); status != http.StatusBadRequest {
		t.Fatalf("bad ping: %d", status)
	}
}
