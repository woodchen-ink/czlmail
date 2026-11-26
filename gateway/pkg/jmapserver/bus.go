package jmapserver

import (
	"sync"
	"time"
)

// Changes maps accountId → type name → new state, the "changed" member of a StateChange.
type Changes map[string]map[string]string

// Bus collects state changes per user and delivers them to that user's
// EventSource connections and, when push is enabled, to the sink feeding the
// user's push subscriptions. Changes published within the debounce window are
// merged into one StateChange, which may cover several accounts.
type Bus struct {
	window time.Duration
	mu     sync.Mutex
	users  map[string]*userHub
	// wants reports whether the sink wants a user's changes (the user has push
	// subscriptions); it is called with mu held. sink gets each flushed batch.
	wants func(userID string) bool
	sink  func(userID string, changed Changes, mails []NewMail)
}

// userHub exists while the user has connections or a pending flush.
type userHub struct {
	subs    map[*subscription]struct{}
	pending Changes
	mails   []NewMail
	timer   *time.Timer
}

// subscription is one EventSource connection.
type subscription struct {
	userID string
	types  map[string]bool // nil: all types
	notify chan struct{}   // capacity 1: "pending has something"
	done   chan struct{}   // closed by EndUser / CloseAll
	once   sync.Once

	mu      sync.Mutex
	pending Changes
}

// NewBus returns a bus merging changes over window.
func NewBus(window time.Duration) *Bus {
	return &Bus{window: window, users: map[string]*userHub{}}
}

// setSink makes flushes also go to sink for the users wants accepts.
func (b *Bus) setSink(wants func(userID string) bool, sink func(userID string, changed Changes, mails []NewMail)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wants, b.sink = wants, sink
}

// Publish records changed states of the user's accounts. With no connection
// open and no push subscription the changes are dropped: a client always
// resynchronises when it connects.
func (b *Bus) Publish(userID string, changed Changes) {
	b.mu.Lock()
	defer b.mu.Unlock()
	hub := b.hubForPublish(userID)
	if hub == nil {
		return
	}
	if hub.pending == nil {
		hub.pending = Changes{}
	}
	hub.pending.merge(changed)
	b.armLocked(userID, hub)
}

// publishMail queues new messages for the push sink; EventSource connections
// only see the accompanying state changes.
func (b *Bus) publishMail(userID string, mails []NewMail) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.wants == nil || !b.wants(userID) {
		return
	}
	hub := b.hubForPublish(userID)
	hub.mails = append(hub.mails, mails...)
	b.armLocked(userID, hub)
}

// hubForPublish returns the hub collecting the user's changes, creating one
// when only the push sink wants them, or nil when nobody does.
func (b *Bus) hubForPublish(userID string) *userHub {
	hub := b.users[userID]
	if hub != nil && (len(hub.subs) > 0 || hub.timer != nil) {
		return hub
	}
	if b.wants == nil || !b.wants(userID) {
		return nil
	}
	if hub == nil {
		hub = &userHub{subs: map[*subscription]struct{}{}}
		b.users[userID] = hub
	}
	return hub
}

func (b *Bus) armLocked(userID string, hub *userHub) {
	if hub.timer == nil {
		hub.timer = time.AfterFunc(b.window, func() { b.flush(userID, hub) })
	}
}

func (b *Bus) flush(userID string, hub *userHub) {
	b.mu.Lock()
	if b.users[userID] != hub {
		b.mu.Unlock()
		return // the hub was closed meanwhile
	}
	pending, mails := hub.pending, hub.mails
	hub.pending, hub.mails, hub.timer = nil, nil, nil
	subs := make([]*subscription, 0, len(hub.subs))
	for s := range hub.subs {
		subs = append(subs, s)
	}
	if len(hub.subs) == 0 {
		delete(b.users, userID) // kept only for this flush
	}
	sink := b.sink
	if sink != nil && b.wants != nil && !b.wants(userID) {
		sink = nil
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.deliver(pending)
	}
	if sink != nil && (len(pending) > 0 || len(mails) > 0) {
		sink(userID, pending, mails)
	}
}

func (b *Bus) subscribe(userID string, types map[string]bool) *subscription {
	s := &subscription{userID: userID, types: types, notify: make(chan struct{}, 1), done: make(chan struct{})}
	b.mu.Lock()
	defer b.mu.Unlock()
	hub := b.users[userID]
	if hub == nil {
		hub = &userHub{subs: map[*subscription]struct{}{}}
		b.users[userID] = hub
	}
	hub.subs[s] = struct{}{}
	return s
}

func (b *Bus) unsubscribe(s *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	hub := b.users[s.userID]
	if hub == nil {
		return
	}
	delete(hub.subs, s)
	if len(hub.subs) == 0 && hub.timer == nil {
		b.dropLocked(s.userID, hub)
	}
}

func (b *Bus) dropLocked(userID string, hub *userHub) {
	if hub.timer != nil {
		hub.timer.Stop()
	}
	delete(b.users, userID)
}

// EndUser closes all of the user's connections and returns how many there were.
// A pending flush still reaches the push sink.
func (b *Bus) EndUser(userID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	hub := b.users[userID]
	if hub == nil {
		return 0
	}
	for s := range hub.subs {
		s.close()
	}
	n := len(hub.subs)
	clear(hub.subs)
	if hub.timer == nil {
		b.dropLocked(userID, hub)
	}
	return n
}

// CloseAll closes every connection.
func (b *Bus) CloseAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for userID, hub := range b.users {
		for s := range hub.subs {
			s.close()
		}
		b.dropLocked(userID, hub)
	}
}

func (s *subscription) close() { s.once.Do(func() { close(s.done) }) }

// deliver merges the changes this connection asked for; it never blocks.
func (s *subscription) deliver(c Changes) {
	f := c.filter(s.types)
	if len(f) == 0 {
		return
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = Changes{}
	}
	s.pending.merge(f)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *subscription) take() Changes {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.pending
	s.pending = nil
	return c
}

// merge copies other into c; a later state for the same type wins.
func (c Changes) merge(other Changes) {
	for acc, types := range other {
		if len(types) == 0 {
			continue
		}
		dst := c[acc]
		if dst == nil {
			dst = make(map[string]string, len(types))
			c[acc] = dst
		}
		for t, st := range types {
			dst[t] = st
		}
	}
}

// filter returns a copy of c with only the given types; nil types keeps all.
func (c Changes) filter(types map[string]bool) Changes {
	out := Changes{}
	for acc, ts := range c {
		for t, st := range ts {
			if types != nil && !types[t] {
				continue
			}
			if out[acc] == nil {
				out[acc] = map[string]string{}
			}
			out[acc][t] = st
		}
	}
	return out
}
