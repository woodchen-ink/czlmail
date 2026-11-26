package jmapserver

import "sync"

// userLimiter caps concurrent operations per user; over the cap it refuses
// rather than queues, as RFC 8620 expects for maxConcurrentRequests.
type userLimiter struct {
	max    int
	mu     sync.Mutex
	active map[string]int
}

func newUserLimiter(max int) *userLimiter {
	return &userLimiter{max: max, active: map[string]int{}}
}

func (l *userLimiter) acquire(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[userID] >= l.max {
		return false
	}
	l.active[userID]++
	return true
}

func (l *userLimiter) release(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[userID] <= 1 {
		delete(l.active, userID)
		return
	}
	l.active[userID]--
}
