package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/model"
)

func newRecorder(t *testing.T, now *time.Time) *Recorder {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close(db) })
	r := New(db)
	r.now = func() time.Time { return *now }
	return r
}

func TestHistogramQuantile(t *testing.T) {
	var h Histogram
	if _, ok := h.Quantile(0.5); ok {
		t.Fatal("empty histogram has a quantile")
	}
	for range 90 {
		h.observe(80 * time.Millisecond) // bucket ≤100
	}
	for range 10 {
		h.observe(1500 * time.Millisecond) // bucket 1000–2000
	}
	if p50, _ := h.Quantile(0.5); p50 < 0 || p50 > 100 {
		t.Fatalf("p50 = %d", p50)
	}
	if p95, _ := h.Quantile(0.95); p95 < 1000 || p95 > 2000 {
		t.Fatalf("p95 = %d", p95)
	}
	var slow Histogram
	slow.observe(time.Hour)
	if v, _ := slow.Quantile(0.95); v != LatencyBounds[len(LatencyBounds)-1] {
		t.Fatalf("overflow quantile = %d", v)
	}
	if got := decodeHistogram(h.encode()); got != h {
		t.Fatalf("round trip: %v != %v", got, h)
	}
	// A longer stored list folds into the overflow bucket.
	if got := decodeHistogram("1,0,0,0,0,0,0,0,0,0,2,3"); got[0] != 1 || got[numBuckets-1] != 5 {
		t.Fatalf("decode long: %v", got)
	}
}

func TestRecordFlushQuery(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 30, 0, 0, time.UTC)
	r := newRecorder(t, &now)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				r.UpstreamRequest("a1")
			}
		}()
	}
	wg.Wait()
	r.RateLimited("a1")
	r.PushLatency("a1", 200*time.Millisecond)
	r.UpstreamRequest("a2")
	r.UpstreamRequest("") // ignored

	// Unflushed counts are visible.
	hours, err := r.Query([]string{"a1"}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil || len(hours) != 1 || hours[0].Requests != 200 || hours[0].RateLimited != 1 {
		t.Fatalf("pending query: %+v %v", hours, err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	// A second flush adds to the stored row rather than replacing it.
	r.UpstreamRequest("a1")
	r.PushLatency("a1", 3*time.Second)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	// And a pending count on top of stored ones is merged once.
	r.UpstreamRequest("a1")
	hours, _ = r.Query([]string{"a1", "a2"}, now.Add(-time.Hour), now.Add(time.Hour))
	got := map[string]Hour{}
	for _, h := range hours {
		got[h.AccountID] = h
	}
	if a1 := got["a1"]; a1.Requests != 202 || a1.Push.count() != 2 || !a1.Hour.Equal(now.Truncate(time.Hour)) {
		t.Fatalf("a1: %+v", a1)
	}
	if got["a2"].Requests != 1 {
		t.Fatalf("a2: %+v", got["a2"])
	}

	var rows int64
	r.db.Model(&model.UpstreamMetricHour{}).Count(&rows)
	if rows != 2 {
		t.Fatalf("rows = %d", rows)
	}
	if err := r.Forget("a2"); err != nil {
		t.Fatal(err)
	}
	r.db.Model(&model.UpstreamMetricHour{}).Count(&rows)
	if rows != 1 {
		t.Fatalf("rows after forget = %d", rows)
	}
}

func TestNilRecorder(t *testing.T) {
	var r *Recorder
	r.UpstreamRequest("a")
	r.RateLimited("a")
	r.PushLatency("a", time.Second)
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if tr, err := r.Trend([]string{"a"}, "24h", 5); err != nil || len(tr.Total) != 24 {
		t.Fatalf("nil trend: %v %v", tr, err)
	}
}

func TestPrune(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	r := newRecorder(t, &now)
	old := now.Add(-Retention - time.Hour)
	r.db.Create(&model.UpstreamMetricHour{AccountID: "a", Hour: old, Requests: 1})
	r.db.Create(&model.UpstreamMetricHour{AccountID: "a", Hour: now.Add(-time.Hour), Requests: 1})
	n, err := r.Prune()
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
}

func TestTrend(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 30, 0, 0, time.UTC)
	r := newRecorder(t, &now)
	cur := now.Truncate(time.Hour)
	// Seven upstreams; a<i> sends i+1 requests per hour in the last 3 hours.
	ids := []string{}
	for i := range 7 {
		id := fmt.Sprintf("a%d", i)
		ids = append(ids, id)
		for h := range 3 {
			r.db.Create(&model.UpstreamMetricHour{AccountID: id, Hour: cur.Add(-time.Duration(h) * time.Hour), Requests: int64(i + 1)})
		}
	}
	// Previous window: 30 hours ago.
	r.db.Create(&model.UpstreamMetricHour{AccountID: "a0", Hour: cur.Add(-30 * time.Hour), Requests: 9, RateLimited: 2})
	r.PushLatency("a6", 400*time.Millisecond)
	// Not asked for: excluded.
	r.db.Create(&model.UpstreamMetricHour{AccountID: "zz", Hour: cur, Requests: 1000})

	tr, err := r.Trend(ids, "24h", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Total) != 24 || !tr.To.Equal(cur.Add(time.Hour)) || !tr.Total[23].T.Equal(cur) {
		t.Fatalf("window: %d points, to %v, last %v", len(tr.Total), tr.To, tr.Total[23].T)
	}
	// 3 hours × (1+…+7) = 84
	if tr.Summary.Requests != 84 || tr.Previous.Requests != 9 || tr.Previous.RateLimited != 2 {
		t.Fatalf("summary %+v previous %+v", tr.Summary, tr.Previous)
	}
	if tr.Total[23].Requests != 28 || tr.Total[20].Requests != 0 {
		t.Fatalf("total points: %+v %+v", tr.Total[23], tr.Total[20])
	}
	if tr.Total[23].PushP50Ms == nil || tr.Total[22].PushP50Ms != nil {
		t.Fatal("latency should be set only where pushes happened")
	}
	if len(tr.Series) != 4 || tr.Series[0].AccountID != "a6" || tr.Series[3].AccountID != OtherSeries {
		t.Fatalf("series: %+v", tr.Series)
	}
	if tr.Series[3].Totals.Requests != 3*(1+2+3+4) {
		t.Fatalf("other: %+v", tr.Series[3].Totals)
	}
	if _, err := r.Trend(ids, "1y", 3); err != ErrUnknownRange {
		t.Fatalf("bad range: %v", err)
	}
}
