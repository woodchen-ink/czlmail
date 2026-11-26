// Package cron runs the gateway's periodic jobs.
package cron

import (
	"context"
	"log/slog"
	"time"
)

// Pruner deletes rows that are past their retention.
type Pruner func() (int64, error)

// StartCleanup runs the pruners once at startup and then every interval until ctx ends.
func StartCleanup(ctx context.Context, log *slog.Logger, interval time.Duration, jobs map[string]Pruner) {
	run := func() {
		for name, prune := range jobs {
			n, err := prune()
			if err != nil {
				log.Error("cleanup failed", "job", name, "err", err)
			} else if n > 0 {
				log.Info("cleanup", "job", name, "deleted", n)
			}
		}
	}
	go func() {
		run()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}
