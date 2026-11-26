package jmapserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// memPushStore is an in-memory jmapserver.PushStore.
type memPushStore struct {
	mu   sync.Mutex
	subs map[string]jmapserver.PushSubscription
}

func (m *memPushStore) ListPush(_ context.Context, userID string) ([]*jmapserver.PushSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*jmapserver.PushSubscription
	for _, s := range m.subs {
		if s.UserID == userID {
			c := s
			out = append(out, &c)
		}
	}
	slices.SortFunc(out, func(a, b *jmapserver.PushSubscription) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *memPushStore) CreatePush(_ context.Context, s *jmapserver.PushSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[s.ID] = *s
	return nil
}

func (m *memPushStore) UpdatePush(_ context.Context, s *jmapserver.PushSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[s.ID] = *s
	return nil
}

func (m *memPushStore) DeletePush(_ context.Context, userID string, ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if m.subs[id].UserID == userID {
			delete(m.subs, id)
		}
	}
	return nil
}

func (m *memPushStore) PushUsers(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.subs {
		if !slices.Contains(out, s.UserID) {
			out = append(out, s.UserID)
		}
	}
	return out, nil
}

func (m *memPushStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs)
}

// relayReq is one message the fake CZL Connect relay received.
type relayReq struct {
	header http.Header
	body   []byte
}

// relay stands in for the CZL Connect push relay (POST /api/push/p/{id}).
type relay struct {
	ts     *httptest.Server
	reqs   chan relayReq
	mu     sync.Mutex
	status []int // answered in order; then 201
}

func newRelay(t *testing.T) *relay {
	r := &relay{reqs: make(chan relayReq, 64)}
	r.ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		status := http.StatusCreated
		if len(r.status) > 0 {
			status, r.status = r.status[0], r.status[1:]
		}
		r.mu.Unlock()
		if req.Method != http.MethodPost || !strings.HasPrefix(req.URL.Path, "/api/push/p/") {
			status = http.StatusNotFound
		}
		r.reqs <- relayReq{header: req.Header.Clone(), body: body}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.ts.Close)
	return r
}

func (r *relay) url(id string) string { return r.ts.URL + "/api/push/p/" + id }

func (r *relay) answer(status ...int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = append(r.status, status...)
}

func (r *relay) next(t *testing.T) relayReq {
	t.Helper()
	select {
	case m := <-r.reqs:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no push message arrived")
		return relayReq{}
	}
}

func (r *relay) none(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case m := <-r.reqs:
		t.Fatalf("unexpected push message: %q", m.body)
	case <-time.After(wait):
	}
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type pushEnv struct {
	*env
	store *memPushStore
	relay *relay
	clock *clock
	keys  deviceKeys
}

const (
	inboxID   = "mInbox"
	archiveID = "mArchive"
)

// newPushEnv is newEnv with push enabled. Alice's a1 is a mail account; a2 is not.
func newPushEnv(t *testing.T) *pushEnv {
	t.Helper()
	e := newEnv(t, 30*time.Millisecond)
	us := aliceSession(jmapserver.UpstreamOK)
	us.Accounts["a1"].AccountCapabilities[jmapserver.MailCapability] = map[string]any{}
	e.accounts.set("usr_alice", us)
	e.srv.RegisterCapability(jmapserver.MailCapability, nil)

	r := newRelay(t)
	clk := &clock{now: time.Now()}
	store := &memPushStore{subs: map[string]jmapserver.PushSubscription{}}
	err := e.srv.EnablePush(context.Background(), jmapserver.PushOptions{
		Store:                store,
		AllowPrivateNetworks: func() bool { return true }, // the fake relay listens on 127.0.0.1
		Transport:            r.ts.Client().Transport.(*http.Transport),
		RetryBase:            10 * time.Millisecond,
		Now:                  clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &pushEnv{env: e, store: store, relay: r, clock: clk, keys: newDeviceKeys(t)}
}

// using is what czlmail sends with PushSubscription calls (jmapx.uses).
const pushUsing = `["urn:ietf:params:jmap:core","urn:ietf:params:jmap:mail","urn:ietf:params:jmap:emailpush"]`

func (e *pushEnv) call(method string, args any) map[string]any {
	e.t.Helper()
	b, _ := json.Marshal(args)
	status, _, body := e.post(fmt.Sprintf(`{"using":%s,"methodCalls":[[%q,%s,"c0"]]}`, pushUsing, method, b))
	if status != http.StatusOK {
		e.t.Fatalf("%s: %d %s", method, status, body)
	}
	r := responses(e.t, body)
	if r[0].Name != method {
		e.t.Fatalf("%s answered %s %v", method, r[0].Name, r[0].Args)
	}
	return r[0].Args
}

// czlmailFilter is the filter czlmail's pushTargets builds: inbox, not from one of my addresses.
func czlmailFilter(own ...string) map[string]any {
	var not []any
	for _, a := range own {
		not = append(not, map[string]any{"from": a})
	}
	return map[string]any{"operator": "AND", "conditions": []any{
		map[string]any{"inMailbox": inboxID},
		map[string]any{"operator": "NOT", "conditions": not},
	}}
}

// createLikeCzlmail creates a subscription with the object syncPush sends.
func (e *pushEnv) createLikeCzlmail(device string, expires time.Time) (string, map[string]any) {
	e.t.Helper()
	res := e.call("PushSubscription/set", map[string]any{"create": map[string]any{"s": map[string]any{
		"deviceClientId": device,
		"url":            e.relay.url("r1"),
		"keys":           map[string]string{"p256dh": e.keys.p256dh(), "auth": e.keys.authB64()},
		"types":          []string{},
		"expires":        expires.UTC().Format(time.RFC3339),
		"emailPush": map[string]any{"a1": map[string]any{
			"filter":     czlmailFilter("alice@example.com"),
			"properties": []string{"id", "from", "subject", "preview"},
			"urgency":    "high",
		}},
	}}})
	created, _ := res["created"].(map[string]any)
	s, _ := created["s"].(map[string]any)
	if s == nil {
		e.t.Fatalf("not created: %v", res["notCreated"])
	}
	return s["id"].(string), s
}

// openMessage decrypts a relay message like the iOS app and checks the headers.
func (e *pushEnv) openMessage(m relayReq) map[string]any {
	e.t.Helper()
	if m.header.Get("Content-Encoding") != "aes128gcm" || m.header.Get("TTL") == "" {
		e.t.Fatalf("headers %v", m.header)
	}
	plain, err := decryptLikeIOS(m.body, e.keys)
	if err != nil {
		e.t.Fatalf("decrypt: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(plain, &obj); err != nil {
		e.t.Fatalf("decode %s: %v", plain, err)
	}
	return obj
}

// verify does what czlmail's awaitVerification does and returns the subscription id.
func (e *pushEnv) verify(id string) {
	e.t.Helper()
	v := e.openMessage(e.relay.next(e.t))
	if v["@type"] != "PushVerification" || v["pushSubscriptionId"] != id || v["verificationCode"] == "" {
		e.t.Fatalf("verification %v", v)
	}
	res := e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{"verificationCode": v["verificationCode"]}}})
	if _, ok := res["updated"].(map[string]any)[id]; !ok {
		e.t.Fatalf("verification rejected: %v", res)
	}
}

func newMail(id, mailbox, fromName, fromAddr, subject, preview string) jmapserver.NewMail {
	from := []jmapserver.EmailAddress{{Name: fromName, Email: fromAddr}}
	return jmapserver.NewMail{
		AccountID: "a1", MailboxIDs: []string{mailbox}, From: from, Subject: subject,
		To:    []jmapserver.EmailAddress{{Email: "alice@example.com"}},
		Email: map[string]any{"id": id, "threadId": "T" + id, "from": from, "subject": subject, "preview": preview, "receivedAt": "2026-11-26T08:00:00Z"},
		State: "s-" + id,
	}
}

// mailNotice is czlmail-mobile's MailNotice.parse (CzlKit/Sources/CzlPush/MailPush.swift):
// @type, accountId and emails are required; the notification shows the first email.
type mailNotice struct{ title, body, actionID, accountID string }

func parseMailNotice(plain []byte, signIn string) (mailNotice, bool) {
	var obj struct {
		Type      *string `json:"@type"`
		AccountID *string `json:"accountId"`
		Emails    *[]struct {
			ID   *string `json:"id"`
			From *[]struct {
				Name  *string `json:"name"`
				Email *string `json:"email"`
			} `json:"from"`
			Subject *string `json:"subject"`
			Preview *string `json:"preview"`
		} `json:"emails"`
	}
	if json.Unmarshal(plain, &obj) != nil || obj.Type == nil || obj.AccountID == nil || obj.Emails == nil ||
		*obj.Type != "EmailPush" || len(*obj.Emails) == 0 {
		return mailNotice{}, false
	}
	email := (*obj.Emails)[0]
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	title := "新邮件"
	if email.From != nil && len(*email.From) > 0 {
		sender := (*email.From)[0]
		for _, c := range []string{strings.TrimSpace(deref(sender.Name)), strings.TrimSpace(deref(sender.Email))} {
			if c != "" {
				title = c
				break
			}
		}
	}
	subject := strings.TrimSpace(deref(email.Subject))
	if subject == "" {
		subject = "(无主题)"
	}
	body := subject
	if preview := strings.TrimSpace(deref(email.Preview)); preview != "" {
		body += "\n" + preview
	}
	if n := len(*obj.Emails); n > 1 {
		body += fmt.Sprintf("\n另有 %d 封新邮件", n-1)
	}
	account := *obj.AccountID
	if signIn != "" {
		account = signIn + "~" + account
	}
	action := ""
	if id := deref(email.ID); id != "" {
		action = account + "/" + id
	}
	return mailNotice{title: title, body: body, actionID: action, accountID: account}, true
}

func TestPushSessionCapabilities(t *testing.T) {
	e := newPushEnv(t)
	_, _, body := e.get("/jmap/session", "alice")
	var sess struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		Accounts     map[string]struct {
			AccountCapabilities map[string]json.RawMessage `json:"accountCapabilities"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}
	if string(sess.Capabilities[jmapserver.EmailPushCapability]) != "{}" {
		t.Fatalf("session emailpush %s", sess.Capabilities[jmapserver.EmailPushCapability])
	}
	var acc struct {
		FilterConditions []string `json:"filterConditions"`
		MaxFilterDepth   int      `json:"maxFilterDepth"`
		CountProperties  []string `json:"countProperties"`
	}
	if err := json.Unmarshal(sess.Accounts["a1"].AccountCapabilities[jmapserver.EmailPushCapability], &acc); err != nil {
		t.Fatalf("a1 emailpush: %v", err)
	}
	if !slices.Contains(acc.FilterConditions, "inMailbox") || !slices.Contains(acc.FilterConditions, "from") ||
		acc.MaxFilterDepth < 3 || acc.CountProperties == nil || len(acc.CountProperties) != 0 {
		t.Fatalf("a1 emailpush %+v", acc)
	}
	if _, ok := sess.Accounts["a2"].AccountCapabilities[jmapserver.EmailPushCapability]; ok {
		t.Fatal("a2 has no mail but announces emailpush")
	}
}

func TestPushSubscriptionLifecycle(t *testing.T) {
	e := newPushEnv(t)
	id, created := e.createLikeCzlmail("dev1", e.clock.Now().Add(7*24*time.Hour))
	if !strings.HasPrefix(id, "P") || created["expires"] == nil {
		t.Fatalf("created %v", created)
	}

	// Nothing but the verification goes out before the client confirms it.
	e.srv.NewMail("usr_alice", newMail("e0", inboxID, "Bob", "bob@example.net", "Early", ""))
	e.srv.Changes().Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "s1"}})
	first := e.relay.next(t)
	if v := e.openMessage(first); v["@type"] != "PushVerification" {
		t.Fatalf("first message %v", v)
	}
	e.relay.none(t, 150*time.Millisecond)

	// get: no url, no keys, no code until verified.
	got := e.call("PushSubscription/get", map[string]any{"ids": nil})
	list := got["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("list %v", got)
	}
	sub := list[0].(map[string]any)
	if sub["id"] != id || sub["deviceClientId"] != "dev1" || sub["verificationCode"] != nil || sub["url"] != nil || sub["keys"] != nil {
		t.Fatalf("get %v", sub)
	}
	if types, ok := sub["types"].([]any); !ok || len(types) != 0 {
		t.Fatalf("types %v", sub["types"])
	}
	if ep := sub["emailPush"].(map[string]any)["a1"].(map[string]any); ep["urgency"] != "high" {
		t.Fatalf("emailPush %v", ep)
	}

	// A wrong code is refused; the pushed one verifies.
	res := e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{"verificationCode": "nope"}}})
	if ne := res["notUpdated"].(map[string]any)[id].(map[string]any); ne["type"] != "invalidProperties" {
		t.Fatalf("wrong code %v", res)
	}
	plain, _ := decryptLikeIOS(first.body, e.keys)
	var v map[string]any
	_ = json.Unmarshal(plain, &v)
	res = e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{"verificationCode": v["verificationCode"]}}})
	if _, ok := res["updated"].(map[string]any)[id]; !ok {
		t.Fatalf("verify %v", res)
	}

	// New inbox mail from someone else is pushed; types [] means no StateChange.
	e.srv.NewMail("usr_alice",
		newMail("e1", inboxID, "Bob", "bob@example.net", "Lunch?", "Are you free at noon"),
		newMail("e2", inboxID, "", "alice@example.com", "Note to self", ""), // from me: not pushed
		newMail("e3", archiveID, "Carol", "carol@example.net", "Filed", ""), // not in the inbox
	)
	e.srv.Changes().Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "s-e3"}})
	m := e.relay.next(t)
	if m.header.Get("Urgency") != "high" {
		t.Fatalf("urgency %q", m.header.Get("Urgency"))
	}
	plain, err := decryptLikeIOS(m.body, e.keys)
	if err != nil {
		t.Fatal(err)
	}
	notice, ok := parseMailNotice(plain, "")
	if !ok {
		t.Fatalf("iOS cannot parse %s", plain)
	}
	want := mailNotice{title: "Bob", body: "Lunch?\nAre you free at noon", actionID: "a1/e1", accountID: "a1"}
	if notice != want {
		t.Fatalf("notice %+v, want %+v (payload %s)", notice, want, plain)
	}
	var push map[string]any
	_ = json.Unmarshal(plain, &push)
	if push["state"] != "s-e3" || len(push["emails"].([]any)) != 1 {
		t.Fatalf("payload %s", plain)
	}
	if _, hasThread := push["emails"].([]any)[0].(map[string]any)["threadId"]; hasThread {
		t.Fatal("pushed a property that was not asked for")
	}
	e.relay.none(t, 150*time.Millisecond)

	// Renewal as czlmail does it.
	res = e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{
		"emailPush": map[string]any{"a1": map[string]any{"filter": map[string]any{"inMailbox": inboxID}, "properties": []string{"id", "from"}}},
		"expires":   e.clock.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}}})
	upd := res["updated"].(map[string]any)[id].(map[string]any)
	exp, _ := time.Parse(time.RFC3339, upd["expires"].(string))
	if exp.After(e.clock.Now().Add(jmapserver.MaxPushTTL)) {
		t.Fatalf("expires not capped: %v", upd)
	}

	// Destroy.
	res = e.call("PushSubscription/set", map[string]any{"destroy": []string{id, "Pmissing"}})
	if d := res["destroyed"].([]any); len(d) != 1 || d[0] != id {
		t.Fatalf("destroy %v", res)
	}
	if res["notDestroyed"].(map[string]any)["Pmissing"] == nil || e.store.count() != 0 {
		t.Fatalf("destroy %v", res)
	}
	e.srv.NewMail("usr_alice", newMail("e4", inboxID, "Bob", "bob@example.net", "Again", ""))
	e.relay.none(t, 150*time.Millisecond)
}

func TestPushStateChangeAndTypes(t *testing.T) {
	e := newPushEnv(t)
	res := e.call("PushSubscription/set", map[string]any{"create": map[string]any{"s": map[string]any{
		"deviceClientId": "web", "url": e.relay.url("r2"),
		"keys":  map[string]string{"p256dh": e.keys.p256dh(), "auth": e.keys.authB64()},
		"types": []string{"Mailbox"},
	}}})
	id := res["created"].(map[string]any)["s"].(map[string]any)["id"].(string)
	e.verify(id)
	e.srv.Changes().Publish("usr_alice", jmapserver.Changes{"a1": {"Email": "s2", "Mailbox": "m2"}, "a2": {"Email": "x"}})
	sc := e.openMessage(e.relay.next(t))
	want := `{"@type":"StateChange","changed":{"a1":{"Mailbox":"m2"}}}`
	if b, _ := json.Marshal(sc); string(b) != want {
		t.Fatalf("state change %s", b)
	}
	// Mail does not reach a subscription without emailPush.
	e.srv.NewMail("usr_alice", newMail("e1", inboxID, "Bob", "bob@example.net", "Hi", ""))
	e.relay.none(t, 150*time.Millisecond)
}

func TestPushExpiry(t *testing.T) {
	e := newPushEnv(t)
	res := e.call("PushSubscription/set", map[string]any{"create": map[string]any{
		"past": map[string]any{"deviceClientId": "d", "url": e.relay.url("x"), "expires": e.clock.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
	}})
	if ne := res["notCreated"].(map[string]any)["past"].(map[string]any); ne["type"] != "invalidProperties" {
		t.Fatalf("past expiry %v", res)
	}

	id, created := e.createLikeCzlmail("dev1", e.clock.Now().Add(60*24*time.Hour))
	exp, _ := time.Parse(time.RFC3339, created["expires"].(string))
	if d := exp.Sub(e.clock.Now()); d > jmapserver.MaxPushTTL || d < jmapserver.MaxPushTTL-time.Minute {
		t.Fatalf("expires %v from now", d)
	}
	e.verify(id)

	e.clock.advance(jmapserver.MaxPushTTL + time.Second)
	if list := e.call("PushSubscription/get", map[string]any{})["list"].([]any); len(list) != 0 {
		t.Fatalf("expired subscription listed: %v", list)
	}
	e.srv.NewMail("usr_alice", newMail("e1", inboxID, "Bob", "bob@example.net", "Late", ""))
	e.relay.none(t, 150*time.Millisecond)
	res = e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{"expires": nil}}})
	if res["notUpdated"].(map[string]any)[id].(map[string]any)["type"] != "notFound" {
		t.Fatalf("renewed an expired subscription: %v", res)
	}
}

func TestPushGoneAndRetry(t *testing.T) {
	e := newPushEnv(t)
	// The relay fails twice before accepting the verification.
	e.relay.answer(http.StatusServiceUnavailable, http.StatusTooManyRequests)
	id, _ := e.createLikeCzlmail("dev1", e.clock.Now().Add(24*time.Hour))
	e.relay.next(t)
	e.relay.next(t)
	e.verify(id)

	// 410: the device is gone (app removed); the subscription is deleted.
	e.relay.answer(http.StatusGone)
	e.srv.NewMail("usr_alice", newMail("e1", inboxID, "Bob", "bob@example.net", "Hi", ""))
	e.relay.next(t)
	waitFor(t, func() bool { return e.store.count() == 0 })

	// 413 drops the message without retrying and keeps the subscription.
	id, _ = e.createLikeCzlmail("dev2", e.clock.Now().Add(24*time.Hour))
	e.verify(id)
	e.relay.answer(http.StatusRequestEntityTooLarge)
	e.srv.NewMail("usr_alice", newMail("e2", inboxID, "Bob", "bob@example.net", "Hi", ""))
	e.relay.next(t)
	e.relay.none(t, 100*time.Millisecond)
	if e.store.count() != 1 {
		t.Fatal("413 deleted the subscription")
	}
}

func TestPushDropByCredential(t *testing.T) {
	e := newPushEnv(t)
	id, _ := e.createLikeCzlmail("dev1", e.clock.Now().Add(24*time.Hour))
	e.relay.next(t)
	_ = e.store.UpdatePush(context.Background(), func() *jmapserver.PushSubscription {
		subs, _ := e.store.ListPush(context.Background(), "usr_alice")
		subs[0].CredentialID = "ap1"
		return subs[0]
	}())
	if err := e.srv.DropPushSubscriptions(context.Background(), "usr_alice", "ap2"); err != nil || e.store.count() != 1 {
		t.Fatalf("another credential's revocation dropped %s: %v", id, err)
	}
	if err := e.srv.DropPushSubscriptions(context.Background(), "usr_alice", "ap1"); err != nil || e.store.count() != 0 {
		t.Fatalf("revocation kept the subscription: %v", err)
	}
}

func TestPushSetValidation(t *testing.T) {
	e := newPushEnv(t)
	keys := map[string]string{"p256dh": e.keys.p256dh(), "auth": e.keys.authB64()}
	base := func(extra map[string]any) map[string]any {
		o := map[string]any{"deviceClientId": "d", "url": e.relay.url("x"), "keys": keys}
		for k, v := range extra {
			if v == nil {
				delete(o, k)
			} else {
				o[k] = v
			}
		}
		return o
	}
	cases := map[string]struct {
		obj   map[string]any
		props []string
	}{
		"http":         {base(map[string]any{"url": "http://relay.example/p/1"}), []string{"url"}},
		"creds":        {base(map[string]any{"url": "https://u:p@relay.example/p/1"}), []string{"url"}},
		"badKey":       {base(map[string]any{"keys": map[string]string{"p256dh": "AAAA", "auth": e.keys.authB64()}}), []string{"keys"}},
		"shortAuth":    {base(map[string]any{"keys": map[string]string{"p256dh": e.keys.p256dh(), "auth": "AAAA"}}), []string{"keys"}},
		"code":         {base(map[string]any{"verificationCode": "x"}), []string{"verificationCode"}},
		"noURL":        {base(map[string]any{"url": nil}), []string{"deviceClientId", "url"}},
		"unknown":      {base(map[string]any{"color": "red"}), []string{"color"}},
		"emailCount":   {base(map[string]any{"emailCount": map[string]any{}}), []string{"emailCount"}},
		"pushNoKeys":   {base(map[string]any{"keys": nil, "emailPush": map[string]any{"a1": map[string]any{}}}), []string{"emailPush", "keys"}},
		"pushAccount":  {base(map[string]any{"emailPush": map[string]any{"a2": map[string]any{}}}), []string{"emailPush", "emailPush/a2"}},
		"pushFilter":   {base(map[string]any{"emailPush": map[string]any{"a1": map[string]any{"filter": map[string]any{"text": "x"}}}}), []string{"emailPush", "emailPush/a1/filter"}},
		"pushDepth":    {base(map[string]any{"emailPush": map[string]any{"a1": map[string]any{"filter": deepFilter(5)}}}), []string{"emailPush", "emailPush/a1/filter"}},
		"pushBodies":   {base(map[string]any{"emailPush": map[string]any{"a1": map[string]any{"properties": []string{"id", "bodyValues"}}}}), []string{"emailPush", "emailPush/a1/properties"}},
		"pushUrgency":  {base(map[string]any{"emailPush": map[string]any{"a1": map[string]any{"urgency": "now"}}}), []string{"emailPush", "emailPush/a1/urgency"}},
		"pushTypeless": {base(map[string]any{"types": []string{""}}), []string{"types"}},
	}
	create := map[string]any{}
	for name, c := range cases {
		create[name] = c.obj
	}
	res := e.call("PushSubscription/set", map[string]any{"create": create})
	if res["created"] != nil {
		t.Fatalf("created %v", res["created"])
	}
	nc := res["notCreated"].(map[string]any)
	for name, c := range cases {
		se, _ := nc[name].(map[string]any)
		var props []string
		for _, p := range asSlice(se["properties"]) {
			props = append(props, p.(string))
		}
		if se["type"] != "invalidProperties" || !slices.Equal(props, c.props) {
			t.Errorf("%s: %v, want properties %v", name, se, c.props)
		}
	}
	if e.store.count() != 0 {
		t.Fatal("an invalid subscription was stored")
	}

	// emailPush is unknown without the capability in using.
	b, _ := json.Marshal(map[string]any{"create": map[string]any{"s": base(map[string]any{"emailPush": map[string]any{"a1": map[string]any{}}})}})
	_, _, body := e.post(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["PushSubscription/set",` + string(b) + `,"c0"]]}`)
	if r := responses(t, body); r[0].Args["notCreated"] == nil {
		t.Fatalf("emailPush accepted without the capability: %s", body)
	}

	// Immutable properties.
	id, _ := e.createLikeCzlmail("dev1", e.clock.Now().Add(time.Hour))
	res = e.call("PushSubscription/set", map[string]any{"update": map[string]any{id: map[string]any{"url": e.relay.url("y")}}})
	if res["notUpdated"].(map[string]any)[id].(map[string]any)["type"] != "invalidProperties" {
		t.Fatalf("url changed: %v", res)
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func deepFilter(depth int) map[string]any {
	f := map[string]any{"inMailbox": inboxID}
	for range depth - 1 {
		f = map[string]any{"operator": "AND", "conditions": []any{f}}
	}
	return f
}

func TestPushQuota(t *testing.T) {
	e := newPushEnv(t)
	create := map[string]any{}
	for i := range jmapserver.MaxPushSubscriptions + 2 {
		create[fmt.Sprintf("s%02d", i)] = map[string]any{"deviceClientId": "d", "url": e.relay.url("x")}
	}
	res := e.call("PushSubscription/set", map[string]any{"create": create})
	if n := len(res["created"].(map[string]any)); n != jmapserver.MaxPushSubscriptions {
		t.Fatalf("created %d", n)
	}
	for _, se := range res["notCreated"].(map[string]any) {
		if se.(map[string]any)["type"] != "overQuota" {
			t.Fatalf("notCreated %v", se)
		}
	}
	// Subscriptions without keys get an unencrypted verification.
	m := e.relay.next(t)
	if m.header.Get("Content-Encoding") != "" || !strings.Contains(string(m.body), `"PushVerification"`) {
		t.Fatalf("plain verification %v %s", m.header, m.body)
	}
}

func TestPushURLGuard(t *testing.T) {
	for _, u := range []string{
		"https://127.0.0.1/p", "https://[::1]/p", "https://10.1.2.3/p", "https://192.168.1.1/p", "https://169.254.169.254/latest",
		"https://100.64.0.1/p", "https://[fd00::1]/p", "https://[2002:7f00:1::1]/p", "https://[::ffff:127.0.0.1]/p", "https://localhost/p", "https://a.localhost/p",
		"http://connect.czl.net/api/push/p/1", "https:///p", "ftp://connect.czl.net/x",
	} {
		if err := jmapserver.CheckPushURL(u, false); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	for _, u := range []string{"https://connect.czl.net/api/push/p/abc", "https://8.8.8.8/p", "https://[2606:4700::1111]/p"} {
		if err := jmapserver.CheckPushURL(u, false); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	if jmapserver.CheckPushURL("https://127.0.0.1/p", true) != nil {
		t.Error("private address refused although allowed")
	}

	// The connect-time check catches names that resolve to private addresses.
	r := newRelay(t)
	host := strings.Replace(r.ts.URL, "127.0.0.1", "localhost", 1)
	client := jmapserver.NewPushClient(r.ts.Client().Transport.(*http.Transport), func() bool { return false })
	_, err := client.Post(host+"/api/push/p/1", "application/octet-stream", strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), jmapserver.ErrPrivateAddress.Error()) {
		t.Fatalf("dial to loopback: %v", err)
	}
	select {
	case <-r.reqs:
		t.Fatal("the request reached a loopback server")
	default:
	}
}
