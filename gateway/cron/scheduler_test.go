package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestSchedulerRunsAndRecords(t *testing.T) {
	s := NewScheduler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	s.Add(Task{Name: "slow", Interval: time.Hour, Run: func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			close(started)
			<-release
		}
		return "done", nil
	}})
	s.Add(PruneTask("prune", time.Hour, func() (int64, error) { return 0, errors.New("boom") }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	<-started
	if _, err := s.RunNow("slow"); !errors.Is(err, ErrTaskRunning) {
		t.Fatalf("overlapping run: %v", err)
	}
	close(release)
	var run *Run
	var err error
	for range 100 {
		if run, err = s.RunNow("slow"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !run.OK || !run.Manual || run.Result != "done" {
		t.Fatalf("manual run: %+v %v", run, err)
	}
	if _, err := s.RunNow("nope"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown: %v", err)
	}
	for range 100 {
		if st := s.List(); st[1].LastRun != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := s.List()
	if len(st[0].History) != 2 || !st[0].History[0].Manual || st[0].NextRunAt == nil || st[0].IntervalSeconds != 3600 {
		t.Fatalf("slow status: %+v", st[0])
	}
	if st[1].LastRun == nil || st[1].LastRun.OK || st[1].LastRun.Error != "boom" || st[1].LastRun.Result != "deleted 0" {
		t.Fatalf("prune status: %+v", st[1].LastRun)
	}
}
