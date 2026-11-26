package metrics

import (
	"cmp"
	"errors"
	"slices"
	"time"
)

// Ranges are the trend windows the admin offers.
var Ranges = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// ErrUnknownRange is returned for a range name not in Ranges.
var ErrUnknownRange = errors.New("unknown range")

// OtherSeries is the AccountID of the series merging the upstreams beyond the top N.
const OtherSeries = "other"

// Point is one hour of a trend. Latencies are null for an hour without pushes.
type Point struct {
	T           time.Time `json:"t"`
	Requests    int64     `json:"requests"`
	RateLimited int64     `json:"rateLimited"`
	PushCount   int64     `json:"pushCount"`
	PushP50Ms   *int64    `json:"pushP50Ms"`
	PushP95Ms   *int64    `json:"pushP95Ms"`
}

// Totals sums a window; latencies are quantiles over all pushes in it.
type Totals struct {
	Requests    int64  `json:"requests"`
	RateLimited int64  `json:"rateLimited"`
	PushCount   int64  `json:"pushCount"`
	PushP50Ms   *int64 `json:"pushP50Ms"`
	PushP95Ms   *int64 `json:"pushP95Ms"`
}

// Series is one upstream's hourly points (or OtherSeries).
type Series struct {
	AccountID string  `json:"accountId"`
	Totals    Totals  `json:"totals"`
	Points    []Point `json:"points"`
}

// Trend is the hourly activity of a set of upstreams over a window ending with the
// current hour. Every hour of the window has a point, zero when nothing happened.
type Trend struct {
	Range string    `json:"range"`
	From  time.Time `json:"from"` // inclusive, UTC hour
	To    time.Time `json:"to"`   // exclusive: the end of the current hour
	// Total merges all upstreams; Series has the top upstreams by requests, then
	// OtherSeries for the rest.
	Total  []Point  `json:"total"`
	Series []Series `json:"series"`
	// Summary sums the window; Previous the window of equal length before it.
	Summary  Totals `json:"summary"`
	Previous Totals `json:"previous"`
}

// Trend returns the hourly trend of the accounts over rangeName, keeping the top
// upstreams by requests as their own series.
func (r *Recorder) Trend(accountIDs []string, rangeName string, top int) (*Trend, error) {
	length, ok := Ranges[rangeName]
	if !ok {
		return nil, ErrUnknownRange
	}
	to := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	if r != nil {
		to = r.now().UTC().Truncate(time.Hour).Add(time.Hour)
	}
	from := to.Add(-length)
	hours, err := r.Query(accountIDs, from.Add(-length), to)
	if err != nil {
		return nil, err
	}
	n := int(length / time.Hour)
	t := &Trend{Range: rangeName, From: from, To: to}

	total := newBuckets(n)
	prev := &Hour{}
	per := map[string][]Hour{}
	for _, h := range hours {
		if h.Hour.Before(from) {
			mergeHour(prev, &h)
			continue
		}
		i := int(h.Hour.Sub(from) / time.Hour)
		mergeHour(&total[i], &h)
		if per[h.AccountID] == nil {
			per[h.AccountID] = newBuckets(n)
		}
		mergeHour(&per[h.AccountID][i], &h)
	}
	t.Total = points(total, from)
	t.Summary = totals(total)
	t.Previous = totals([]Hour{*prev})

	type ranked struct {
		id    string
		hours []Hour
		sum   Totals
	}
	all := make([]ranked, 0, len(per))
	for id, hs := range per {
		all = append(all, ranked{id, hs, totals(hs)})
	}
	slices.SortFunc(all, func(a, b ranked) int {
		if c := cmp.Compare(b.sum.Requests, a.sum.Requests); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	var other []Hour
	for i, s := range all {
		if top > 0 && i >= top && len(all) > top+1 {
			if other == nil {
				other = newBuckets(n)
			}
			for j := range s.hours {
				mergeHour(&other[j], &s.hours[j])
			}
			continue
		}
		t.Series = append(t.Series, Series{AccountID: s.id, Totals: s.sum, Points: points(s.hours, from)})
	}
	if other != nil {
		t.Series = append(t.Series, Series{AccountID: OtherSeries, Totals: totals(other), Points: points(other, from)})
	}
	if t.Series == nil {
		t.Series = []Series{}
	}
	return t, nil
}

func newBuckets(n int) []Hour { return make([]Hour, n) }

func points(hours []Hour, from time.Time) []Point {
	out := make([]Point, len(hours))
	for i := range hours {
		h := &hours[i]
		out[i] = Point{
			T: from.Add(time.Duration(i) * time.Hour), Requests: h.Requests, RateLimited: h.RateLimited,
			PushCount: h.Push.count(), PushP50Ms: quantile(&h.Push, 0.5), PushP95Ms: quantile(&h.Push, 0.95),
		}
	}
	return out
}

func totals(hours []Hour) Totals {
	var sum Hour
	for i := range hours {
		mergeHour(&sum, &hours[i])
	}
	return Totals{
		Requests: sum.Requests, RateLimited: sum.RateLimited, PushCount: sum.Push.count(),
		PushP50Ms: quantile(&sum.Push, 0.5), PushP95Ms: quantile(&sum.Push, 0.95),
	}
}

func quantile(h *Histogram, q float64) *int64 {
	v, ok := h.Quantile(q)
	if !ok {
		return nil
	}
	return &v
}
