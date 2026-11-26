package jmapserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Delivery of push messages. Each subscription has its own queue drained by one
// goroutine at a time, so messages to one device stay in order and a slow push
// service never holds up other subscriptions or the change bus.

const (
	pushQueueLimit   = 64 // per subscription; the oldest messages go first
	pushMaxAttempts  = 5
	pushMaxRetryWait = 10 * time.Minute
	pushTTLSeconds   = 86400 // how long the push service may hold a message
	pushListTimeout  = 10 * time.Second
)

type pushMsg struct {
	kind    string // "verification", "state" or "email", for logs
	body    []byte // JSON, encrypted when sent
	urgency string
}

type pushQueue struct {
	sub  *PushSubscription // latest snapshot: url and keys never change, the rest is not needed here
	msgs []pushMsg
}

type pusher struct {
	srv          *Server
	store        PushStore
	client       *http.Client
	allowPrivate func() bool
	maxPayload   int
	retryBase    time.Duration
	now          func() time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// users holds the users that may have subscriptions, with a generation bumped
	// on every create so a concurrent flush does not forget a new subscription.
	users  map[string]uint64
	queues map[string]*pushQueue // by subscription id
}

// wants reports whether changes of the user must be collected for push. It is
// called with the bus locked and must not call back into the bus.
func (p *pusher) wants(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.users[userID]
	return ok
}

func (p *pusher) addUser(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users[userID]++
}

// flush is the bus sink: it pushes one debounced batch of the user's changes
// and new messages to every verified, unexpired subscription.
func (p *pusher) flush(userID string, changes Changes, mails []NewMail) {
	if p.ctx.Err() != nil {
		return
	}
	p.mu.Lock()
	gen, ok := p.users[userID]
	p.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, pushListTimeout)
	defer cancel()
	subs, err := p.store.ListPush(ctx, userID)
	if err != nil {
		p.srv.log.Error("push: list subscriptions", "user", userID, "err", err)
		return
	}
	if len(subs) == 0 {
		p.mu.Lock()
		if p.users[userID] == gen {
			delete(p.users, userID)
		}
		p.mu.Unlock()
		return
	}

	byAccount := map[string][]*NewMail{}
	for i := range mails {
		byAccount[mails[i].AccountID] = append(byAccount[mails[i].AccountID], &mails[i])
	}
	now := p.now()
	for _, sub := range subs {
		if !sub.Verified || !sub.Expires.After(now) {
			continue
		}
		if c := changes.filter(typeSet(sub.Types)); len(c) > 0 {
			body, _ := json.Marshal(stateChange{Type: "StateChange", Changed: c})
			p.enqueue(sub, pushMsg{kind: "state", body: body})
		}
		for accountID, cfg := range sub.EmailPush {
			filter, err := parseEmailFilter(cfg.Filter)
			if err != nil {
				continue // validated when stored; a stored filter that no longer parses pushes nothing
			}
			var matched []*NewMail
			state := ""
			for _, m := range byAccount[accountID] {
				if filter.match(m) {
					matched = append(matched, m)
				}
				if m.State != "" {
					state = m.State
				}
			}
			if len(matched) == 0 {
				continue
			}
			for _, body := range buildEmailPushes(accountID, state, matched, cfg.Properties, p.maxPayload) {
				p.enqueue(sub, pushMsg{kind: "email", body: body, urgency: cfg.Urgency})
			}
		}
	}
}

// typeSet turns a subscription's types into a Changes filter: nil keeps all, empty keeps none.
func typeSet(types []string) map[string]bool {
	if types == nil {
		return nil
	}
	m := make(map[string]bool, len(types))
	for _, t := range types {
		m[t] = true
	}
	return m
}

// verify pushes the PushVerification of a new subscription.
func (p *pusher) verify(sub *PushSubscription) {
	body, _ := json.Marshal(pushVerification{
		Type: "PushVerification", PushSubscriptionID: sub.ID, VerificationCode: sub.VerificationCode,
	})
	p.enqueue(sub, pushMsg{kind: "verification", body: body})
}

func (p *pusher) enqueue(sub *PushSubscription, m pushMsg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	q := p.queues[sub.ID]
	if q == nil {
		q = &pushQueue{}
		p.queues[sub.ID] = q
		p.wg.Add(1)
		go p.drain(sub.ID)
	}
	q.sub = sub
	q.msgs = append(q.msgs, m)
	if n := len(q.msgs) - pushQueueLimit; n > 0 {
		q.msgs = q.msgs[n:]
	}
}

// forget drops the queue of a destroyed subscription.
func (p *pusher) forget(ids ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		if q := p.queues[id]; q != nil {
			q.msgs = nil
		}
	}
}

func (p *pusher) drain(id string) {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		q := p.queues[id]
		if len(q.msgs) == 0 || p.ctx.Err() != nil {
			delete(p.queues, id)
			p.mu.Unlock()
			return
		}
		m := q.msgs[0]
		q.msgs = q.msgs[1:]
		sub := q.sub
		p.mu.Unlock()

		if p.deliver(sub, m) == deliveryGone {
			p.srv.log.Info("push: subscription gone, deleting it", "user", sub.UserID, "subscription", sub.ID)
			ctx, cancel := context.WithTimeout(context.Background(), pushListTimeout)
			if err := p.store.DeletePush(ctx, sub.UserID, sub.ID); err != nil {
				p.srv.log.Error("push: delete gone subscription", "subscription", sub.ID, "err", err)
			}
			cancel()
			p.mu.Lock()
			delete(p.queues, id)
			p.mu.Unlock()
			return
		}
	}
}

type deliveryResult int

const (
	deliveryOK deliveryResult = iota
	deliveryDropped
	deliveryGone
)

// deliver posts one message, retrying transient failures with exponential backoff.
func (p *pusher) deliver(sub *PushSubscription, m pushMsg) deliveryResult {
	for attempt := 0; ; attempt++ {
		status, retryAfter, err := p.post(sub, m)
		switch {
		case err == nil && status >= 200 && status < 300:
			return deliveryOK
		case status == http.StatusNotFound || status == http.StatusGone:
			return deliveryGone
		case err == nil && status != http.StatusTooManyRequests && status < 500:
			// 400, 403 (push disabled for now), 413 (too large) and the like: retrying sends the same thing.
			p.srv.log.Warn("push: message rejected", "subscription", sub.ID, "kind", m.kind, "status", status)
			return deliveryDropped
		}
		if p.ctx.Err() != nil {
			return deliveryDropped
		}
		if attempt+1 >= pushMaxAttempts {
			p.srv.log.Warn("push: giving up on message", "subscription", sub.ID, "kind", m.kind,
				"status", status, "err", err, "attempts", attempt+1)
			return deliveryDropped
		}
		wait := p.retryBase << attempt
		if retryAfter > wait {
			wait = retryAfter
		}
		wait = min(wait, pushMaxRetryWait)
		select {
		case <-p.ctx.Done():
			return deliveryDropped
		case <-time.After(wait):
		}
	}
}

// post sends one message and returns the status and any Retry-After.
func (p *pusher) post(sub *PushSubscription, m pushMsg) (int, time.Duration, error) {
	body := m.body
	contentType := "application/json; charset=utf-8"
	if sub.P256DH != nil {
		enc, err := encryptPush(sub.P256DH, sub.Auth, m.body)
		if err != nil {
			return 0, 0, err
		}
		body, contentType = enc, "application/octet-stream"
	}
	req, err := http.NewRequestWithContext(p.ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", contentType)
	if sub.P256DH != nil {
		req.Header.Set("Content-Encoding", "aes128gcm")
	}
	req.Header.Set("TTL", strconv.Itoa(pushTTLSeconds))
	if m.urgency != "" {
		req.Header.Set("Urgency", m.urgency)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	var retryAfter time.Duration
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	return resp.StatusCode, retryAfter, nil
}

// close stops deliveries and waits for the queues to wind down.
func (p *pusher) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}
