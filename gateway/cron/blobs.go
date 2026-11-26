// Package cron runs the gateway's periodic jobs.
package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// blobCleanupEvery is how often expired uploads are removed.
const blobCleanupEvery = time.Hour

// RunBlobCleanup removes uploads unused for jmapserver.UploadTTL, once at start
// and then hourly, until ctx is cancelled.
func RunBlobCleanup(ctx context.Context, log *slog.Logger, store *jmapserver.UploadStore) {
	t := time.NewTicker(blobCleanupEvery)
	defer t.Stop()
	for {
		if n, err := store.Cleanup(time.Now()); err != nil {
			log.Error("blob cleanup", "err", err)
		} else if n > 0 {
			log.Info("blob cleanup", "removed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
