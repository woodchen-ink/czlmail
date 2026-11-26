package upstream

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// Status is an upstream's state as the Session shows it.
type Status struct {
	State string // jmapserver.Upstream*
	Code  int    // 0 when ok or syncing without error
	Since time.Time
	// Error is the last failure, for logs and the admin only.
	Error string
	// Progress is the first import, nil once it finished.
	Progress *jmapserver.InitialSync
	// LastSync is when the last sync round completed; zero before the first.
	LastSync time.Time
}

// Events receive what a Runner does. Calls are made from the Runner's goroutine
// and must not block for long.
type Events interface {
	// StatusChanged reports a new State/Code (not every progress tick).
	StatusChanged(accountID string, st Status)
	// Progress reports first-import progress.
	Progress(accountID string, done, total int)
	// Changed reports new states after an index commit.
	Changed(accountID string, states map[string]string)
}

// Timing of a Runner; the zero value uses the defaults.
type Timing struct {
	BackoffMin       time.Duration // 5 s
	BackoffMax       time.Duration // 5 min
	ThrottledMin     time.Duration // 1 min
	PollTick         time.Duration // 30 s: how often due folders are looked for
	HintDebounce     time.Duration // 300 ms
	IdleAfter        time.Duration // 30 min without push connections
	DeepSyncInterval time.Duration // 24 h
}

func (t *Timing) fill() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&t.BackoffMin, 5*time.Second)
	def(&t.BackoffMax, 5*time.Minute)
	def(&t.ThrottledMin, time.Minute)
	def(&t.PollTick, 30*time.Second)
	def(&t.HintDebounce, 300*time.Millisecond)
	def(&t.IdleAfter, 30*time.Minute)
	def(&t.DeepSyncInterval, 24*time.Hour)
}

// Runner keeps one upstream in sync until its context ends.
type Runner struct {
	id     string
	up     Upstream
	events Events
	log    *slog.Logger
	timing Timing

	wake    chan struct{} // a client connected: sync everything now
	metrics Metrics

	mu         sync.Mutex
	status     Status
	clients    int
	lastClient time.Time
}

// NewRunner returns a Runner for up. The caller owns up and closes it after Run returns.
func NewRunner(accountID string, up Upstream, events Events, log *slog.Logger, timing Timing) *Runner {
	timing.fill()
	return &Runner{id: accountID, up: up, events: events, log: log, timing: timing, wake: make(chan struct{}, 1), lastClient: time.Now()}
}

// SetMetrics records throttling and push latency; call it before Run.
func (r *Runner) SetMetrics(m Metrics) { r.metrics = m }

// Upstream is the running upstream (for reads and writes by JMAP methods).
func (r *Runner) Upstream() Upstream { return r.up }

// Status returns the current status.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	if st.Progress != nil {
		p := *st.Progress
		st.Progress = &p
	}
	return st
}

// SetClients tells the runner how many push connections the user has. A new
// connection triggers a full status round; none for IdleAfter slows polling.
func (r *Runner) SetClients(n int) {
	r.mu.Lock()
	grew := n > r.clients
	r.clients = n
	r.lastClient = time.Now()
	r.mu.Unlock()
	if grew {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

func (r *Runner) idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clients == 0 && time.Since(r.lastClient) > r.timing.IdleAfter
}

func (r *Runner) setStatus(state string, code int, err error) {
	r.mu.Lock()
	changed := r.status.State != state || r.status.Code != code
	if changed {
		r.status.State, r.status.Code, r.status.Since = state, code, time.Now().UTC()
	}
	if err != nil {
		r.status.Error = err.Error()
	} else if state == jmapserver.UpstreamOK {
		r.status.Error = ""
	}
	if state != jmapserver.UpstreamSyncing {
		r.status.Progress = nil
	}
	st := r.status
	r.mu.Unlock()
	if changed {
		r.events.StatusChanged(r.id, st)
	}
}

func (r *Runner) progress(done, total int) {
	r.mu.Lock()
	state := r.status.State
	r.mu.Unlock()
	if state != jmapserver.UpstreamSyncing {
		// A retry of the first sync got through: it is importing again.
		r.setStatus(jmapserver.UpstreamSyncing, 0, nil)
	}
	r.mu.Lock()
	r.status.Progress = &jmapserver.InitialSync{Done: done, Total: total}
	r.mu.Unlock()
	r.events.Progress(r.id, done, total)
}

// Run syncs and watches until ctx ends. Authentication failures stop it: the
// upstream waits in authRequired until the owner changes its credentials, which
// restarts the runner.
func (r *Runner) Run(ctx context.Context) {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		err := r.session(ctx, &backoff)
		if ctx.Err() != nil {
			return
		}
		ue := Classify("sync", err)
		if ue.Kind == KindThrottled && r.metrics != nil {
			r.metrics.RateLimited(r.id)
		}
		state, code := ue.Status()
		r.log.Warn("upstream failed", "account", r.id, "status", state, "code", code, "err", err)
		r.setStatus(state, code, err)
		if ue.Kind == KindAuth {
			<-ctx.Done()
			return
		}
		backoff = nextBackoff(backoff, ue.Kind, r.timing)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func nextBackoff(cur time.Duration, kind ErrorKind, t Timing) time.Duration {
	min := t.BackoffMin
	if kind == KindThrottled {
		min = t.ThrottledMin
	}
	if cur < min {
		return min
	}
	return minDur(cur*2, t.BackoffMax)
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// errWatchEnded means the watcher returned without error while the session went on.
var errWatchEnded = errors.New("upstream: watcher stopped")

// session is one connected period: a full round, then watching and polling
// until something fails.
func (r *Runner) session(ctx context.Context, backoff *time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The first attempt says syncing; a retry keeps its error status until it
	// imports something (progress) or completes, so the Session does not flap.
	if !r.up.Index().Ready() && r.Status().State == "" {
		r.setStatus(jmapserver.UpstreamSyncing, 0, nil)
	}
	if err := r.sync(ctx, SyncRequest{Full: true, Progress: r.progress}); err != nil {
		return err
	}
	r.setStatus(jmapserver.UpstreamOK, 0, nil)
	*backoff = 0

	hints := make(chan Hint, 64)
	watchErr := make(chan error, 1)
	go func() {
		err := r.up.Watch(ctx, func(h Hint) {
			select {
			case hints <- h:
			default: // full: a sync is coming anyway
			}
		})
		if err == nil && ctx.Err() == nil {
			err = errWatchEnded
		}
		watchErr <- err
	}()

	poll := time.NewTicker(r.timing.PollTick)
	defer poll.Stop()
	lastDeep := time.Now()
	if m := r.up.Index().Meta(); m.LastFullSync != nil {
		lastDeep = *m.LastFullSync
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-watchErr:
			return err
		case <-r.wake:
			if err := r.sync(ctx, SyncRequest{Full: true}); err != nil {
				return err
			}
		case h := <-hints:
			// Push latency: from the upstream's hint to the change being published.
			hinted := time.Now()
			req := r.collectHints(ctx, h, hints)
			states, err := r.syncStates(ctx, req)
			if len(states) > 0 && r.metrics != nil {
				r.metrics.PushLatency(r.id, time.Since(hinted))
			}
			if err != nil {
				return err
			}
		case <-poll.C:
			req := SyncRequest{Idle: r.idle()}
			if time.Since(lastDeep) >= r.timing.DeepSyncInterval {
				req.Deep, req.Full = true, true
				lastDeep = time.Now()
			}
			if err := r.sync(ctx, req); err != nil {
				return err
			}
		}
	}
}

// collectHints waits HintDebounce for more hints and merges them into one request.
func (r *Runner) collectHints(ctx context.Context, first Hint, more <-chan Hint) SyncRequest {
	names := map[string]bool{}
	all := false
	add := func(h Hint) {
		if h.Mailbox == "" {
			all = true
			return
		}
		names[h.Mailbox] = true
	}
	add(first)
	t := time.NewTimer(r.timing.HintDebounce)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return SyncRequest{}
		case h := <-more:
			add(h)
		case <-t.C:
			req := SyncRequest{Full: all, Idle: r.idle()}
			for n := range names {
				req.Mailboxes = append(req.Mailboxes, n)
			}
			return req
		}
	}
}

func (r *Runner) sync(ctx context.Context, req SyncRequest) error {
	_, err := r.syncStates(ctx, req)
	return err
}

// syncStates runs one round, publishes the changed states and returns them.
func (r *Runner) syncStates(ctx context.Context, req SyncRequest) (map[string]string, error) {
	states, err := r.up.Sync(ctx, req)
	if len(states) > 0 {
		r.events.Changed(r.id, states)
	}
	if err == nil {
		r.mu.Lock()
		r.status.LastSync = time.Now().UTC()
		r.mu.Unlock()
	}
	return states, err
}
