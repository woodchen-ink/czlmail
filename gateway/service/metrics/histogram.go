package metrics

import (
	"strconv"
	"strings"
	"time"
)

// LatencyBounds are the upper bounds of the push latency buckets in milliseconds.
// A final overflow bucket counts everything above the last bound.
var LatencyBounds = [...]int64{100, 250, 500, 1_000, 2_000, 5_000, 10_000, 30_000, 60_000, 300_000}

const numBuckets = len(LatencyBounds) + 1

// Histogram counts push latencies per bucket of LatencyBounds.
type Histogram [numBuckets]int64

func (h *Histogram) observe(d time.Duration) {
	ms := d.Milliseconds()
	for i, b := range LatencyBounds {
		if ms <= b {
			h[i]++
			return
		}
	}
	h[numBuckets-1]++
}

func (h *Histogram) add(o *Histogram) {
	for i := range h {
		h[i] += o[i]
	}
}

func (h *Histogram) count() int64 {
	var n int64
	for _, c := range h {
		n += c
	}
	return n
}

// Quantile estimates the q-quantile (0 < q ≤ 1) in milliseconds by linear
// interpolation inside the bucket that holds it; ok is false without samples.
// Values in the overflow bucket are reported as the last bound.
func (h *Histogram) Quantile(q float64) (ms int64, ok bool) {
	total := h.count()
	if total == 0 {
		return 0, false
	}
	rank := q * float64(total)
	var cum int64
	for i, c := range h {
		if c == 0 {
			continue
		}
		if float64(cum+c) >= rank {
			lower := int64(0)
			if i > 0 {
				lower = LatencyBounds[i-1]
			}
			if i == numBuckets-1 {
				return lower, true
			}
			frac := (rank - float64(cum)) / float64(c)
			return lower + int64(frac*float64(LatencyBounds[i]-lower)), true
		}
		cum += c
	}
	return LatencyBounds[len(LatencyBounds)-1], true
}

func (h *Histogram) encode() string {
	if h.count() == 0 {
		return ""
	}
	parts := make([]string, numBuckets)
	for i, c := range h {
		parts[i] = strconv.FormatInt(c, 10)
	}
	return strings.Join(parts, ",")
}

// decodeHistogram reads PushBuckets. Extra buckets (from a longer bound list) are
// folded into the overflow bucket; unparsable values count as zero.
func decodeHistogram(s string) Histogram {
	var h Histogram
	if s == "" {
		return h
	}
	for i, p := range strings.Split(s, ",") {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			continue
		}
		h[min(i, numBuckets-1)] += n
	}
	return h
}
