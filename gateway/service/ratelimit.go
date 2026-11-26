package service

import (
	"sync"
	"time"
)

// Limit is a token bucket: Burst attempts at once, then one more every Every.
type Limit struct {
	Burst int
	Every time.Duration
}

// Limits for credential checks. Only failures keep their token; a success refunds it,
// so signed-in users and correct passwords never run into them.
var (
	LimitLoginUser = Limit{Burst: 10, Every: time.Minute}      // per email, admin sign-in
	LimitLoginIP   = Limit{Burst: 30, Every: 10 * time.Second} // per client IP, admin sign-in
	LimitBasicUser = Limit{Burst: 10, Every: time.Minute}      // per username, JMAP Basic
	LimitBasicIP   = Limit{Burst: 30, Every: 10 * time.Second} // per client IP, JMAP Basic
	LimitSetupIP   = Limit{Burst: 10, Every: time.Minute}      // per client IP, setup link
	LimitSignupIP  = Limit{Burst: 5, Every: 10 * time.Minute}  // per client IP, open registration (successes count too)
)

// RateLimiter keeps token buckets in memory, keyed by dimension and value.
type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
	limit  Limit
}

// NewRateLimiter returns an empty limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: map[string]*bucket{}, now: time.Now}
}

// reservation is the tokens taken for one attempt, refunded if the attempt succeeds.
type reservation struct {
	l    *RateLimiter
	keys []string
}

type limitKey struct {
	key   string
	limit Limit
}

// take takes one token from each bucket, or none and a RateLimitError when any is empty.
func (l *RateLimiter) take(keys ...limitKey) (*reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	var wait time.Duration
	for _, k := range keys {
		b := l.refill(k, now)
		if b.tokens < 1 {
			wait = max(wait, time.Duration((1-b.tokens)*float64(k.limit.Every)))
		}
	}
	if wait > 0 {
		return nil, &RateLimitError{RetryAfter: wait}
	}
	res := &reservation{l: l}
	for _, k := range keys {
		l.buckets[k.key].tokens--
		res.keys = append(res.keys, k.key)
	}
	return res, nil
}

// refund gives the tokens back: the attempt succeeded.
func (r *reservation) refund() {
	if r == nil {
		return
	}
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	for _, k := range r.keys {
		if b := r.l.buckets[k]; b != nil {
			b.tokens = min(b.tokens+1, float64(b.limit.Burst))
		}
	}
}

func (l *RateLimiter) refill(k limitKey, now time.Time) *bucket {
	b := l.buckets[k.key]
	if b == nil {
		b = &bucket{tokens: float64(k.limit.Burst), last: now, limit: k.limit}
		l.buckets[k.key] = b
		return b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(float64(k.limit.Burst), b.tokens+float64(elapsed)/float64(k.limit.Every))
		b.last = now
	}
	return b
}

// sweep drops buckets that have refilled completely, at most once a minute.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if b.tokens+float64(now.Sub(b.last))/float64(b.limit.Every) >= float64(b.limit.Burst) {
			delete(l.buckets, key)
		}
	}
}
