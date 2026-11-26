package imap

import (
	"context"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
)

// idleConnTTL: a pooled connection unused for longer is closed rather than reused;
// servers drop idle sessions after 30 minutes and NAT tables sooner.
const idleConnTTL = 10 * time.Minute

// pool hands out at most n work connections.
type pool struct {
	sem  chan struct{}
	dial func(context.Context) (*imapc.Client, error)

	mu     sync.Mutex
	idle   []pooled
	closed bool
	all    map[*imapc.Client]struct{}
}

type pooled struct {
	c    *imapc.Client
	used time.Time
}

func newPool(n int, dial func(context.Context) (*imapc.Client, error)) *pool {
	return &pool{sem: make(chan struct{}, n), dial: dial, all: map[*imapc.Client]struct{}{}}
}

// get waits for a free slot and returns a connection; release must be called
// once, with broken true when the connection must not be reused.
func (p *pool) get(ctx context.Context) (*imapc.Client, bool, func(broken bool), error) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, false, nil, ctx.Err()
	}
	free := func() { <-p.sem }
	var c *imapc.Client
	reused := false
	p.mu.Lock()
	for len(p.idle) > 0 && c == nil {
		last := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		select {
		case <-last.c.Done():
			delete(p.all, last.c)
			continue
		default:
		}
		if time.Since(last.used) > idleConnTTL {
			delete(p.all, last.c)
			go last.c.Logout(context.Background())
			continue
		}
		c, reused = last.c, true
	}
	p.mu.Unlock()
	if c == nil {
		var err error
		c, err = p.dial(ctx)
		if err != nil {
			free()
			return nil, false, nil, err
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			c.Close()
			free()
			return nil, false, nil, context.Canceled
		}
		p.all[c] = struct{}{}
		p.mu.Unlock()
	}
	var once sync.Once
	release := func(broken bool) {
		once.Do(func() {
			p.mu.Lock()
			if broken || p.closed {
				delete(p.all, c)
				c.Close()
			} else {
				p.idle = append(p.idle, pooled{c: c, used: time.Now()})
			}
			p.mu.Unlock()
			free()
		})
	}
	return c, reused, release, nil
}

func (p *pool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for c := range p.all {
		c.Close()
	}
	p.all = map[*imapc.Client]struct{}{}
	p.idle = nil
}

// bucket is a token bucket for new connections: providers lock out clients that
// reconnect in a tight loop.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	every  time.Duration
	last   time.Time
}

func newBucket(burst int, every time.Duration) *bucket {
	return &bucket{tokens: float64(burst), max: float64(burst), every: every, last: time.Now()}
}

func (b *bucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens = min(b.max, b.tokens+float64(now.Sub(b.last))/float64(b.every))
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) * float64(b.every))
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
