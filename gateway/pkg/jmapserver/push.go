package jmapserver

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// PushOptions enable PushSubscription and new-mail push.
type PushOptions struct {
	Store PushStore
	// AllowPrivateNetworks reports whether push URLs may point at private,
	// loopback and link-local addresses; nil means never.
	AllowPrivateNetworks func() bool
	// Transport is cloned for push requests; nil uses http.DefaultTransport.
	Transport *http.Transport
	// MaxPayload is the plaintext budget of one message; zero means DefaultMaxPushPayload.
	MaxPayload int
	// RetryBase is the first retry delay, doubled on each attempt; zero means 2 seconds.
	RetryBase time.Duration
	// Now is the clock for expiry; nil means time.Now.
	Now func() time.Time
}

// EnablePush registers PushSubscription/get and /set and the emailpush
// capability, and makes the change bus also deliver to push subscriptions.
// Call it once, before serving.
func (s *Server) EnablePush(ctx context.Context, o PushOptions) error {
	if o.Store == nil {
		return errors.New("jmapserver: PushOptions.Store is required")
	}
	if s.push != nil {
		return errors.New("jmapserver: push is already enabled")
	}
	allow := o.AllowPrivateNetworks
	if allow == nil {
		allow = func() bool { return false }
	}
	p := &pusher{
		srv: s, store: o.Store, allowPrivate: allow,
		client:     newPushClient(o.Transport, allow),
		maxPayload: o.MaxPayload, retryBase: o.RetryBase, now: o.Now,
		users: map[string]uint64{}, queues: map[string]*pushQueue{},
	}
	if p.maxPayload <= 0 {
		p.maxPayload = DefaultMaxPushPayload
	}
	if p.retryBase <= 0 {
		p.retryBase = 2 * time.Second
	}
	if p.now == nil {
		p.now = time.Now
	}
	users, err := o.Store.PushUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		p.users[u] = 0
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())

	s.mu.Lock()
	s.push = p
	s.mu.Unlock()
	s.RegisterCapability(EmailPushCapability, nil)
	s.Register("PushSubscription/get", CoreCapability, p.get)
	s.Register("PushSubscription/set", CoreCapability, p.set)
	s.bus.setSink(p.wants, p.flush)
	return nil
}

// NewMail reports messages just delivered into the user's accounts. They are
// pushed to the subscriptions whose emailPush filter they match, batched with
// the user's other changes of the debounce window. The caller still publishes
// the Email state change on Changes() as for any other change.
func (s *Server) NewMail(userID string, mails ...NewMail) {
	if s.push == nil || len(mails) == 0 {
		return
	}
	s.bus.publishMail(userID, mails)
}

// DropPushSubscriptions deletes the user's subscriptions created with the
// credential, or all of them when credentialID is empty (the user was disabled
// or deleted). A revoked device must not keep receiving mail summaries.
func (s *Server) DropPushSubscriptions(ctx context.Context, userID, credentialID string) error {
	if s.push == nil {
		return nil
	}
	subs, err := s.push.store.ListPush(ctx, userID)
	if err != nil {
		return err
	}
	var ids []string
	for _, sub := range subs {
		if credentialID == "" || sub.CredentialID == credentialID {
			ids = append(ids, sub.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.push.store.DeletePush(ctx, userID, ids...); err != nil {
		return err
	}
	s.push.forget(ids...)
	return nil
}
