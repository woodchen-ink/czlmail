package model

import "time"

// UpstreamMetricHour aggregates one upstream's activity over one UTC hour. Rows are
// written by service/metrics (merged in memory, flushed every minute) and kept 30 days.
type UpstreamMetricHour struct {
	AccountID   string    `gorm:"primaryKey;size:64"` // the upstream's JMAP account id
	Hour        time.Time `gorm:"primaryKey;index"`   // start of the hour, UTC
	Requests    int64     `gorm:"not null;default:0"` // requests sent to the upstream
	RateLimited int64     `gorm:"not null;default:0"` // requests the upstream throttled
	PushCount   int64     `gorm:"not null;default:0"` // changes pushed to clients
	// PushBuckets is the push latency histogram as comma-separated counts, one per
	// bucket of metrics.LatencyBounds plus a final overflow bucket.
	PushBuckets string `gorm:"size:256;not null;default:''"`
}
