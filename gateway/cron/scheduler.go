// Package cron runs the gateway's periodic jobs and keeps their recent runs for
// the admin's task center.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/woodchen-ink/go-web-utils/cronutil"
)

// historySize is how many runs of each task are kept in memory.
const historySize = 20

// Task is a job run every Interval, first right after Start.
type Task struct {
	Name     string
	Interval time.Duration
	// Run does the work and returns a short summary for the admin ("deleted 12").
	Run func(ctx context.Context) (string, error)
}

// Pruner deletes rows that are past their retention.
type Pruner func() (int64, error)

// PruneTask wraps a pruner as a task.
func PruneTask(name string, every time.Duration, prune Pruner) Task {
	return Task{Name: name, Interval: every, Run: func(context.Context) (string, error) {
		n, err := prune()
		return fmt.Sprintf("deleted %d", n), err
	}}
}

// Run is one execution of a task.
type Run struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	OK         bool      `json:"ok"`
	Result     string    `json:"result"`
	Error      string    `json:"error"`
	Manual     bool      `json:"manual"`
}

// Status is a task as the task center shows it. History is newest first.
type Status struct {
	Name            string     `json:"name"`
	IntervalSeconds int64      `json:"intervalSeconds"`
	Running         bool       `json:"running"`
	NextRunAt       *time.Time `json:"nextRunAt"`
	LastRun         *Run       `json:"lastRun"`
	History         []Run      `json:"history"`
}

// Errors of RunNow.
var (
	ErrUnknownTask = errors.New("unknown task")
	ErrTaskRunning = errors.New("task is running")
)

type task struct {
	def   Task
	guard cronutil.TickGuard

	mu      sync.Mutex
	running bool
	next    time.Time
	history []Run // oldest first
}

// Scheduler runs tasks on their intervals; a run never overlaps the previous run of
// the same task.
type Scheduler struct {
	log   *slog.Logger
	tasks []*task
	ctx   context.Context
}

// NewScheduler returns an empty scheduler.
func NewScheduler(log *slog.Logger) *Scheduler {
	return &Scheduler{log: log, ctx: context.Background()}
}

// Add registers a task; call it before Start.
func (s *Scheduler) Add(t Task) { s.tasks = append(s.tasks, &task{def: t}) }

// Start runs every task once and then on its interval until ctx ends.
func (s *Scheduler) Start(ctx context.Context) {
	s.ctx = ctx
	for _, t := range s.tasks {
		go s.loop(ctx, t)
	}
}

func (s *Scheduler) loop(ctx context.Context, t *task) {
	tick := time.NewTicker(t.def.Interval)
	defer tick.Stop()
	for {
		t.mu.Lock()
		t.next = time.Now().Add(t.def.Interval)
		t.mu.Unlock()
		s.run(t, false)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// run executes t unless it is already running; it reports whether it ran.
func (s *Scheduler) run(t *task, manual bool) bool {
	return t.guard.TryRun(func() {
		t.mu.Lock()
		t.running = true
		t.mu.Unlock()
		r := Run{StartedAt: time.Now().UTC(), Manual: manual}
		res, err := t.def.Run(s.ctx)
		r.FinishedAt, r.Result, r.OK = time.Now().UTC(), res, err == nil
		if err != nil {
			r.Error = err.Error()
			s.log.Error("task failed", "task", t.def.Name, "err", err)
		}
		t.mu.Lock()
		t.running = false
		t.history = append(t.history, r)
		if len(t.history) > historySize {
			t.history = t.history[len(t.history)-historySize:]
		}
		t.mu.Unlock()
	})
}

// RunNow runs a task immediately and waits for it.
func (s *Scheduler) RunNow(name string) (*Run, error) {
	i := slices.IndexFunc(s.tasks, func(t *task) bool { return t.def.Name == name })
	if i < 0 {
		return nil, ErrUnknownTask
	}
	t := s.tasks[i]
	if !s.run(t, true) {
		return nil, ErrTaskRunning
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	last := t.history[len(t.history)-1]
	return &last, nil
}

// List returns every task's status in registration order.
func (s *Scheduler) List() []Status {
	out := make([]Status, 0, len(s.tasks))
	for _, t := range s.tasks {
		t.mu.Lock()
		st := Status{Name: t.def.Name, IntervalSeconds: int64(t.def.Interval / time.Second), Running: t.running, History: make([]Run, 0, len(t.history))}
		if !t.next.IsZero() {
			next := t.next.UTC()
			st.NextRunAt = &next
		}
		for i := len(t.history) - 1; i >= 0; i-- {
			st.History = append(st.History, t.history[i])
		}
		if len(st.History) > 0 {
			st.LastRun = &st.History[0]
		}
		t.mu.Unlock()
		out = append(out, st)
	}
	return out
}
