// Package metrics counts upstream activity per upstream and per hour: requests sent,
// rate-limit hits and push latency. The upstream code records events through a
// Recorder; the admin overview reads hourly trends back.
//
// Events are merged in memory and flushed to model.UpstreamMetricHour every minute,
// so recording never touches the database. Rows are kept for Retention.
package metrics

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Retention is how long hourly rows are kept.
const Retention = 30 * 24 * time.Hour

// FlushEvery is how often Run writes the in-memory counts to the database.
const FlushEvery = time.Minute

// Hour is one upstream's counts for one UTC hour.
type Hour struct {
	AccountID   string
	Hour        time.Time
	Requests    int64
	RateLimited int64
	Push        Histogram
}

type hourKey struct {
	account string
	hour    int64 // unix seconds of the hour start
}

// Recorder collects upstream events. All methods are safe for concurrent use and
// never block on the database; a nil *Recorder ignores every call.
type Recorder struct {
	db  *gorm.DB
	now func() time.Time

	flushMu sync.Mutex // held by Flush and Query, so a query never sees a flush half done
	mu      sync.Mutex
	pending map[hourKey]*Hour
}

// New returns a recorder writing to db.
func New(db *gorm.DB) *Recorder {
	return &Recorder{db: db, now: time.Now, pending: map[hourKey]*Hour{}}
}

// UpstreamRequest counts one request sent to the upstream accountID (an IMAP command
// round trip, a Gmail or Graph HTTP request). Long-lived IDLE / NOTIFY connections
// are not requests; reconnecting them is.
func (r *Recorder) UpstreamRequest(accountID string) {
	r.update(accountID, func(h *Hour) { h.Requests++ })
}

// RateLimited counts one request the upstream throttled (HTTP 429, Gmail
// rateLimitExceeded, IMAP [THROTTLED] / [LIMIT] and the like).
func (r *Recorder) RateLimited(accountID string) {
	r.update(accountID, func(h *Hour) { h.RateLimited++ })
}

// PushLatency records the time from the upstream reporting a change (IDLE, NOTIFY,
// a provider push or a poll that found it) to publishing the change to clients.
// Call it only when a change was actually published.
func (r *Recorder) PushLatency(accountID string, d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.update(accountID, func(h *Hour) { h.Push.observe(d) })
}

func (r *Recorder) update(accountID string, fn func(*Hour)) {
	if r == nil || accountID == "" {
		return
	}
	hour := r.now().UTC().Truncate(time.Hour)
	k := hourKey{accountID, hour.Unix()}
	r.mu.Lock()
	h := r.pending[k]
	if h == nil {
		h = &Hour{AccountID: accountID, Hour: hour}
		r.pending[k] = h
	}
	fn(h)
	r.mu.Unlock()
}

// Run flushes every FlushEvery until ctx ends, then flushes once more.
func (r *Recorder) Run(ctx context.Context, onError func(error)) {
	t := time.NewTicker(FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := r.Flush(); err != nil && onError != nil {
				onError(err)
			}
			return
		case <-t.C:
			if err := r.Flush(); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

// Flush adds the in-memory counts to the stored rows. On failure the counts go back
// into memory and the next flush retries them.
func (r *Recorder) Flush() error {
	if r == nil {
		return nil
	}
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	batch := r.pending
	r.pending = map[hourKey]*Hour{}
	r.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for _, h := range batch {
			if err := mergeRow(tx, h); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.mu.Lock()
		for k, h := range batch {
			if cur := r.pending[k]; cur != nil {
				mergeHour(h, cur)
			}
			r.pending[k] = h
		}
		r.mu.Unlock()
	}
	return err
}

func mergeRow(tx *gorm.DB, h *Hour) error {
	var row model.UpstreamMetricHour
	err := tx.Where("account_id = ? AND hour = ?", h.AccountID, h.Hour).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		row = model.UpstreamMetricHour{AccountID: h.AccountID, Hour: h.Hour}
	case err != nil:
		return err
	}
	stored := fromRow(&row)
	mergeHour(stored, h)
	row.Requests, row.RateLimited = stored.Requests, stored.RateLimited
	row.PushCount, row.PushBuckets = stored.Push.count(), stored.Push.encode()
	return tx.Save(&row).Error
}

// mergeHour adds src's counts to dst.
func mergeHour(dst, src *Hour) {
	dst.Requests += src.Requests
	dst.RateLimited += src.RateLimited
	dst.Push.add(&src.Push)
}

func fromRow(row *model.UpstreamMetricHour) *Hour {
	return &Hour{
		AccountID: row.AccountID, Hour: row.Hour.UTC(),
		Requests: row.Requests, RateLimited: row.RateLimited, Push: decodeHistogram(row.PushBuckets),
	}
}

// Query returns the hours of the given accounts in [from, to), stored and not yet
// flushed, unordered.
func (r *Recorder) Query(accountIDs []string, from, to time.Time) ([]Hour, error) {
	if r == nil || len(accountIDs) == 0 {
		return nil, nil
	}
	from, to = from.UTC(), to.UTC()
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	var rows []model.UpstreamMetricHour
	if err := r.db.Where("account_id IN ? AND hour >= ? AND hour < ?", accountIDs, from, to).Find(&rows).Error; err != nil {
		return nil, err
	}
	byKey := make(map[hourKey]*Hour, len(rows))
	for i := range rows {
		h := fromRow(&rows[i])
		byKey[hourKey{h.AccountID, h.Hour.Unix()}] = h
	}
	want := make(map[string]bool, len(accountIDs))
	for _, id := range accountIDs {
		want[id] = true
	}
	r.mu.Lock()
	for k, p := range r.pending {
		if !want[k.account] || p.Hour.Before(from) || !p.Hour.Before(to) {
			continue
		}
		if h := byKey[k]; h != nil {
			mergeHour(h, p)
		} else {
			cp := *p
			byKey[k] = &cp
		}
	}
	r.mu.Unlock()
	out := make([]Hour, 0, len(byKey))
	for _, h := range byKey {
		out = append(out, *h)
	}
	return out, nil
}

// Forget deletes everything recorded for an upstream; call it when the upstream is removed.
func (r *Recorder) Forget(accountID string) error {
	if r == nil {
		return nil
	}
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	for k := range r.pending {
		if k.account == accountID {
			delete(r.pending, k)
		}
	}
	r.mu.Unlock()
	return r.db.Where("account_id = ?", accountID).Delete(&model.UpstreamMetricHour{}).Error
}

// Prune deletes rows older than Retention.
func (r *Recorder) Prune() (int64, error) {
	if r == nil {
		return 0, nil
	}
	res := r.db.Where("hour < ?", r.now().UTC().Add(-Retention)).Delete(&model.UpstreamMetricHour{})
	return res.RowsAffected, res.Error
}
