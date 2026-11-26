package gatewaytest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

const gmailAccount = "me@gmail.test"

type gmailEnv struct {
	gw       *Gateway
	cred     Credentials
	provider *oauthtest.Server
	mail     *gmailtest.Server
	api      *gmailtest.API
}

func newGmailEnv(t *testing.T) *gmailEnv {
	t.Helper()
	e := &gmailEnv{provider: oauthtest.New(t)}
	e.mail = gmailtest.New(t, gmailtest.Options{Account: gmailAccount, Tokens: e.provider})
	e.api = gmailtest.NewAPI(t, e.provider, e.mail)
	e.gw = Start(t)
	e.cred = e.gw.NewUser(t)
	return e
}

// up returns the running upstream of acct.
func (e *gmailEnv) up(t *testing.T, acct string) upstream.Upstream {
	t.Helper()
	h, uerr := e.gw.App.Manager.Lookup(acct)
	if h == nil {
		t.Fatalf("lookup %s: %+v", acct, uerr)
	}
	return h.Upstream
}

func (e *gmailEnv) sync(t *testing.T, acct string) {
	t.Helper()
	if _, err := e.up(t, acct).Sync(context.Background(), upstream.SyncRequest{Full: true}); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// mailboxes returns the index's mailboxes of acct by display key: the label
// for label mailboxes, the role otherwise.
func (e *gmailEnv) mailboxes(t *testing.T, acct string) map[string]model.IndexMailbox {
	t.Helper()
	var rows []model.IndexMailbox
	if err := e.up(t, acct).Index().DB().Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]model.IndexMailbox{}
	for _, r := range rows {
		k := r.Label
		if k == "" {
			k = r.Role
		}
		out[k] = r
	}
	return out
}

// emailBySubject finds an indexed email.
func (e *gmailEnv) emailBySubject(t *testing.T, acct, subject string) model.IndexEmail {
	t.Helper()
	var em model.IndexEmail
	if err := e.up(t, acct).Index().DB().Where("subject = ? AND orphaned_from = ''", subject).First(&em).Error; err != nil {
		t.Fatalf("email %q not indexed: %v", subject, err)
	}
	return em
}

// placesOf returns the display keys of the visible mailboxes an email is in.
func (e *gmailEnv) placesOf(t *testing.T, acct, emailID string) []string {
	t.Helper()
	mbs := e.mailboxes(t, acct)
	byID := map[string]string{}
	for k, m := range mbs {
		if !m.Hidden {
			byID[m.ID] = k
		}
	}
	var ms []model.IndexMembership
	e.up(t, acct).Index().DB().Where("email_id = ?", emailID).Find(&ms)
	var out []string
	for _, m := range ms {
		if k, ok := byID[m.MailboxID]; ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func wantPlaces(t *testing.T, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("mailboxes %v, want %v", got, want)
	}
}

func TestGmailOAuthLifecycle(t *testing.T) {
	e := newGmailEnv(t)
	e.mail.MustAdd(gmailtest.Inbox, imaptest.Sample("hello"))
	e.gw.UseFakeGoogle(t, e.mail, e.api, e.provider)

	// start: the authorisation URL carries PKCE, offline access, consent and the hint.
	var start struct{ URL string }
	admin := e.gw.UserAdmin(t, e.cred)
	admin.Do(t, "POST", "/api/admin/upstreams/oauth/google/start", map[string]any{"email": gmailAccount, "proxy": map[string]any{"mode": "none"}}, &start)
	u, _ := url.Parse(start.URL)
	q := u.Query()
	for k, v := range map[string]string{"access_type": "offline", "prompt": "consent", "login_hint": gmailAccount,
		"code_challenge_method": "S256", "scope": "https://mail.google.com/", "client_id": TestClientID,
		"redirect_uri": e.gw.URL + "/oauth/google/callback", "response_type": "code"} {
		if q.Get(k) != v {
			t.Errorf("auth URL %s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("auth URL without state or PKCE: %s", start.URL)
	}

	// Another session cannot finish this flow, and does not burn the state.
	other := e.gw.Login(t, e.cred.Email, e.cred.Password)
	if r := other.Browse(t, start.URL); r.Get("oauthError") != "invalid_oauth_state" {
		t.Fatalf("callback from another session: %v", r)
	}
	r := admin.Browse(t, start.URL)
	acct := r.Get("added")
	if acct == "" {
		t.Fatalf("callback: %v", r)
	}
	// The state is single-use.
	if r := admin.Browse(t, start.URL); r.Get("oauthError") != "invalid_oauth_state" {
		t.Fatalf("second use of a state: %v", r)
	}
	e.gw.WaitUpstream(t, e.cred, acct, "ok", 30*time.Second)

	var exchanged bool
	for _, f := range e.provider.Requests() {
		if f.Get("grant_type") == "authorization_code" && f.Get("code_verifier") != "" {
			exchanged = true
		}
	}
	if !exchanged {
		t.Fatal("no authorization_code grant with a PKCE verifier")
	}

	// Session: a Gmail account takes any number of mailboxes per email.
	s := e.gw.session(t, e.cred)
	var mc map[string]any
	_ = json.Unmarshal(s.Accounts[acct].AccountCapabilities["urn:ietf:params:jmap:mail"], &mc)
	if v, ok := mc["maxMailboxesPerEmail"]; !ok || v != nil {
		t.Fatalf("maxMailboxesPerEmail = %v, want null", v)
	}

	// Adding the same address again and re-authorising keep the accountId.
	if r := e.gw.AuthorizeOAuth(t, e.cred, "google", map[string]any{"proxy": map[string]any{"mode": "none"}}); r.Get("reauthorized") != acct {
		t.Fatalf("same address again: %v, want reauthorized=%s", r, acct)
	}
	if r := e.gw.ReauthorizeOAuth(t, e.cred, acct); r.Get("reauthorized") != acct {
		t.Fatalf("reauth: %v", r)
	}
	e.gw.WaitUpstream(t, e.cred, acct, "ok", 30*time.Second)

	// A plain IMAP upstream of the same user (the fake with a password).
	imapMail := gmailtest.New(t, gmailtest.Options{Account: "other@imap.test", Password: "pw"})
	imapMail.MustAdd(gmailtest.Inbox, imaptest.Sample("imap side"))
	host, port := splitAddr(imapMail.Addr())
	imapID := e.gw.CreateUpstream(t, e.cred, map[string]any{
		"name": "other", "email": "other@imap.test",
		"imap":  map[string]any{"host": host, "port": port, "tls": "plain", "username": "other@imap.test", "password": "pw"},
		"proxy": map[string]any{"mode": "none"},
	})
	e.gw.WaitUpstream(t, e.cred, imapID, "ok", 30*time.Second)

	// Refresh: an expired access token is refreshed on the next connection.
	before := countGrants(e.provider, "refresh_token")
	e.provider.ExpireAccessTokens()
	e.mail.DropConnections()
	e.sync(t, acct)
	if countGrants(e.provider, "refresh_token") == before {
		t.Fatal("expired token was not refreshed")
	}

	// The grant is revoked: invalid_grant → authRequired (40110); IMAP carries on.
	e.provider.FailAll("invalid_grant")
	e.provider.ExpireAccessTokens()
	e.mail.DropConnections()
	got := e.gw.WaitUpstream(t, e.cred, acct, "authRequired", 30*time.Second)
	if got.Status.Code == nil || *got.Status.Code != 40110 {
		t.Fatalf("authRequired code %v", got.Status.Code)
	}
	imapMail.MustAdd(gmailtest.Inbox, imaptest.Sample("still syncing"))
	e.sync(t, imapID)
	if st := e.gw.GetUpstream(t, e.cred, imapID).Status.State; st != "ok" {
		t.Fatalf("IMAP upstream is %s while Gmail needs re-auth", st)
	}
	e.emailBySubject(t, imapID, "still syncing")

	// Re-authorising fixes it, same accountId.
	e.provider.FailAll("")
	if r := e.gw.ReauthorizeOAuth(t, e.cred, acct); r.Get("reauthorized") != acct {
		t.Fatalf("reauth after invalid_grant: %v", r)
	}
	e.gw.WaitUpstream(t, e.cred, acct, "ok", 30*time.Second)

	// Delete revokes the grant.
	admin.Do(t, "DELETE", "/api/admin/upstreams/"+acct, nil, nil)
	deadline := time.Now().Add(10 * time.Second)
	for len(e.provider.Revoked()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("deleting the upstream did not revoke its token")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.HasPrefix(e.provider.Revoked()[0], "rt-") {
		t.Fatalf("revoked %v, want the refresh token", e.provider.Revoked())
	}
}

func countGrants(p *oauthtest.Server, grant string) int {
	n := 0
	for _, f := range p.Requests() {
		if f.Get("grant_type") == grant {
			n++
		}
	}
	return n
}

func TestGmailOAuthErrors(t *testing.T) {
	e := newGmailEnv(t)
	admin := e.gw.UserAdmin(t, e.cred)
	// No app yet.
	if code, _ := admin.Call(t, "POST", "/api/admin/upstreams/oauth/google/start", map[string]any{}, nil); code != 40931 {
		t.Fatalf("start without an app: %d", code)
	}
	if code, _ := admin.Call(t, "POST", "/api/admin/upstreams/oauth/nope/start", map[string]any{}, nil); code != 40431 {
		t.Fatalf("unknown provider: %d", code)
	}
	// Only site admins see the app settings.
	if code, _ := admin.Call(t, "GET", "/api/admin/oauth-apps", nil, nil); code != 40301 {
		t.Fatalf("oauth-apps as a user: %d", code)
	}
	e.gw.UseFakeGoogle(t, e.mail, e.api, e.provider)
	var apps []struct {
		Provider    string   `json:"provider"`
		Configured  bool     `json:"configured"`
		HasSecret   bool     `json:"hasSecret"`
		RedirectURI string   `json:"redirectUri"`
		Scopes      []string `json:"scopes"`
	}
	e.gw.Owner(t).Do(t, "GET", "/api/admin/oauth-apps", nil, &apps)
	i := slices.IndexFunc(apps, func(a struct {
		Provider    string   `json:"provider"`
		Configured  bool     `json:"configured"`
		HasSecret   bool     `json:"hasSecret"`
		RedirectURI string   `json:"redirectUri"`
		Scopes      []string `json:"scopes"`
	}) bool {
		return a.Provider == "google"
	})
	if i < 0 || !apps[i].Configured || !apps[i].HasSecret || apps[i].RedirectURI != e.gw.URL+"/oauth/google/callback" {
		t.Fatalf("oauth-apps: %+v", apps)
	}
	// The user refuses on Google's page.
	var start struct{ URL string }
	admin.Do(t, "POST", "/api/admin/upstreams/oauth/google/start", map[string]any{"proxy": map[string]any{"mode": "none"}}, &start)
	u, _ := url.Parse(start.URL)
	if r := admin.Browse(t, e.gw.URL+"/oauth/google/callback?error=access_denied&state="+u.Query().Get("state")); r.Get("oauthError") != "oauth_denied" {
		t.Fatalf("denied: %v", r)
	}
	// The code exchange fails.
	e.provider.FailNext("invalid_grant")
	if r := e.gw.AuthorizeOAuth(t, e.cred, "google", map[string]any{"proxy": map[string]any{"mode": "none"}}); r.Get("oauthError") != "oauth_exchange_failed" || r.Get("code") != "50230" {
		t.Fatalf("failed exchange: %v", r)
	}
}

func TestGmailLabelsAndWrites(t *testing.T) {
	e := newGmailEnv(t)
	m := e.mail
	m.CreateLabel("Work Projects")
	m.MustAdd(gmailtest.Inbox, imaptest.Sample("in inbox"))
	m.MustAdd("Work Projects", imaptest.Sample("labelled"))
	archived, _ := m.MustAdd(gmailtest.AllMail, imaptest.Sample("archived"))
	m.MustAdd(gmailtest.Trash, imaptest.Sample("trashed"))
	m.MustAdd(gmailtest.Spam, imaptest.Sample("spam"))
	m.MustAdd(gmailtest.Drafts, imaptest.Sample("a draft"), `\Draft`)
	starred, _ := m.MustAdd(gmailtest.Inbox, imaptest.Sample("starred"), `\Flagged`)
	m.SetLabels(starred, []string{gmailtest.LabelImportant}, nil)
	acct := string(e.gw.AddGmailUpstream(t, e.cred, m, e.api, e.provider))
	ctx := context.Background()

	mbs := e.mailboxes(t, acct)
	if all := mbs["all"]; !all.Hidden || all.Virtual {
		t.Fatalf("All Mail row: %+v", all)
	}
	for _, k := range []string{`\Inbox`, `\Sent`, `\Draft`, "Work Projects", "archive"} {
		if !mbs[k].Virtual {
			t.Errorf("mailbox %q missing or not virtual: %+v", k, mbs[k])
		}
	}
	for _, k := range []string{`\Important`, `\Starred`, "flagged", "important"} {
		if _, ok := mbs[k]; ok {
			t.Errorf("hidden label %q has a mailbox", k)
		}
	}
	if mbs["archive"].Name != "[Gmail]/Archive" || mbs["drafts"].ID != "" && mbs[`\Draft`].Role != "drafts" {
		t.Fatalf("archive %q, drafts role %q", mbs["archive"].Name, mbs[`\Draft`].Role)
	}
	place := func(subject string) []string { return e.placesOf(t, acct, e.emailBySubject(t, acct, subject).ID) }
	wantPlaces(t, place("in inbox"), `\Inbox`)
	wantPlaces(t, place("labelled"), "Work Projects")
	wantPlaces(t, place("archived"), "archive")
	wantPlaces(t, place("trashed"), "trash")
	wantPlaces(t, place("spam"), "junk")
	wantPlaces(t, place("a draft"), `\Draft`)
	wantPlaces(t, place("starred"), `\Inbox`)
	if em := e.emailBySubject(t, acct, "starred"); !em.Flagged || !strings.HasPrefix(em.UpstreamID, "gm:") || !strings.HasPrefix(em.UpstreamThreadID, "gm:") {
		t.Fatalf("starred email: flagged %v, ids %q %q", em.Flagged, em.UpstreamID, em.UpstreamThreadID)
	}

	g := e.up(t, acct)
	ms := g.(upstream.MailboxSetter)
	inbox := e.emailBySubject(t, acct, "in inbox")

	// Add a label with a space: one label "Work Projects", not two (trap 2).
	if _, err := ms.SetMailboxes(ctx, []string{inbox.ID}, []string{mbs["Work Projects"].ID}, nil); err != nil {
		t.Fatal(err)
	}
	gm := m.ByGmMsgID(gmID(t, inbox.UpstreamID))
	if l := m.LabelsOf(gm); !slices.Contains(l, "Work Projects") || slices.Contains(l, "Work") {
		t.Fatalf("labels after add: %v", l)
	}
	wantPlaces(t, place("in inbox"), `\Inbox`, "Work Projects")

	// Archive = only \Inbox goes.
	if _, err := ms.SetMailboxes(ctx, []string{inbox.ID}, []string{mbs["archive"].ID}, []string{mbs[`\Inbox`].ID}); err != nil {
		t.Fatal(err)
	}
	if l := m.LabelsOf(gm); slices.Contains(l, gmailtest.LabelInbox) || !slices.Contains(l, "Work Projects") {
		t.Fatalf("labels after archive: %v", l)
	}
	wantPlaces(t, place("in inbox"), "Work Projects")

	// Move to Trash and back: the labels come back, the id stays.
	if _, err := g.Move(ctx, []string{inbox.ID}, mbs["trash"].ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.LabelsOf(gm), gmailtest.LabelTrash) {
		t.Fatalf("not trashed: %v", m.LabelsOf(gm))
	}
	wantPlaces(t, place("in inbox"), "trash")
	if _, err := ms.SetMailboxes(ctx, []string{inbox.ID}, nil, []string{mbs["trash"].ID}); err != nil {
		t.Fatal(err)
	}
	if l := m.LabelsOf(gm); slices.Contains(l, gmailtest.LabelTrash) || !slices.Contains(l, "Work Projects") {
		t.Fatalf("labels after untrash: %v", l)
	}
	if got := e.emailBySubject(t, acct, "in inbox"); got.ID != inbox.ID {
		t.Fatalf("untrash changed the id: %s → %s", inbox.ID, got.ID)
	}
	wantPlaces(t, place("in inbox"), "Work Projects")

	// Move = exactly one mailbox.
	if _, err := g.Move(ctx, []string{inbox.ID}, mbs[`\Inbox`].ID); err != nil {
		t.Fatal(err)
	}
	wantPlaces(t, place("in inbox"), `\Inbox`)

	// Spam → "not spam" lands in the inbox.
	sp := e.emailBySubject(t, acct, "spam")
	if _, err := ms.SetMailboxes(ctx, []string{sp.ID}, nil, []string{mbs["junk"].ID}); err != nil {
		t.Fatal(err)
	}
	wantPlaces(t, place("spam"), `\Inbox`)

	// Labels changed in Gmail's web UI arrive with the next sync, with new labels.
	m.SetLabels(archived, []string{"Later"}, nil)
	e.sync(t, acct)
	wantPlaces(t, place("archived"), "Later")
	m.SetLabels(archived, nil, []string{"Later"})
	e.sync(t, acct)
	wantPlaces(t, place("archived"), "archive")

	// Destroy: into Trash, then expunged; gone from Gmail.
	tr := e.emailBySubject(t, acct, "archived")
	if _, err := g.Destroy(ctx, []string{tr.ID}); err != nil {
		t.Fatal(err)
	}
	if m.ByGmMsgID(gmID(t, tr.UpstreamID)) != nil {
		t.Fatal("destroyed message still in Gmail")
	}
	var n int64
	g.Index().DB().Model(&model.IndexEmail{}).Where("id = ?", tr.ID).Count(&n)
	if n != 0 {
		t.Fatal("destroyed email still indexed")
	}

	// Label mailboxes: create, rename, delete (its mail goes to Archive).
	id, _, err := g.CreateMailbox(ctx, "", "Receipts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.SetMailboxes(ctx, []string{inbox.ID}, []string{id}, []string{mbs[`\Inbox`].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RenameMailbox(ctx, id, "", "Bills"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.LabelsOf(gm), "Bills") {
		t.Fatalf("labels after rename: %v", m.LabelsOf(gm))
	}
	wantPlaces(t, place("in inbox"), "Bills")
	if _, err := g.DeleteMailbox(ctx, id); err != nil {
		t.Fatal(err)
	}
	wantPlaces(t, place("in inbox"), "archive")
	if _, err := g.DeleteMailbox(ctx, mbs[`\Inbox`].ID); !upstream.IsKind(err, upstream.KindRejected) {
		t.Fatalf("deleting INBOX: %v", err)
	}
}

// Trap 1 and 3: APPEND to Drafts answers with a Drafts UID; the message must be
// found in All Mail by X-GM-MSGID, and \Draft (label) is the \Drafts folder.
func TestGmailAppendDraft(t *testing.T) {
	e := newGmailEnv(t)
	e.mail.MustAdd(gmailtest.Inbox, imaptest.Sample("first in inbox"))
	acct := string(e.gw.AddGmailUpstream(t, e.cred, e.mail, e.api, e.provider))
	ctx := context.Background()
	g := e.up(t, acct)
	mbs := e.mailboxes(t, acct)

	raw := imaptest.Sample("my draft")
	id, _, err := g.Append(ctx, mbs[`\Draft`].ID, raw, []string{"$draft", "$seen"}, time.Time{})
	if err != nil || id == "" {
		t.Fatalf("append: %q %v", id, err)
	}
	em := e.emailBySubject(t, acct, "my draft")
	if em.ID != id || !em.Draft {
		t.Fatalf("appended email %+v, want id %s with $draft", em, id)
	}
	wantPlaces(t, e.placesOf(t, acct, id), `\Draft`)
	var buf bytes.Buffer
	if err := g.FetchRaw(ctx, id, &buf); err != nil || !strings.Contains(buf.String(), "Subject: my draft") {
		t.Fatalf("FetchRaw of the draft: %v %q", err, firstLine(buf.String(), "Subject:"))
	}
	// A sync finds nothing new: the draft was indexed once, at its All Mail UID.
	e.sync(t, acct)
	var n int64
	g.Index().DB().Model(&model.IndexEmail{}).Where("subject = ?", "my draft").Count(&n)
	if n != 1 {
		t.Fatalf("draft indexed %d times", n)
	}
}

func firstLine(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestGmailSearchSendAndIdentities(t *testing.T) {
	e := newGmailEnv(t)
	e.mail.MustAdd(gmailtest.Inbox, imaptest.SampleFrom("ann@example.test", gmailAccount, "quarterly report"))
	e.mail.MustAdd(gmailtest.Inbox, imaptest.SampleFrom("bob@example.test", gmailAccount, "lunch"))
	acct := string(e.gw.AddGmailUpstream(t, e.cred, e.mail, e.api, e.provider))
	ctx := context.Background()
	g := e.up(t, acct)

	// X-GM-RAW search.
	ids, err := g.(upstream.TextSearcher).SearchText(ctx, upstream.SearchFilter{From: "ann@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	report := e.emailBySubject(t, acct, "quarterly report")
	if !slices.Equal(ids, []string{report.ID}) {
		t.Fatalf("search from:ann = %v, want [%s]", ids, report.ID)
	}
	if ids, _ := g.(upstream.TextSearcher).SearchText(ctx, upstream.SearchFilter{Subject: "lunch"}); len(ids) != 1 {
		t.Fatalf("search subject:lunch = %v", ids)
	}

	// Send a draft that answers the report: the sent message takes the draft's id.
	mbs := e.mailboxes(t, acct)
	raw := []byte("From: " + gmailAccount + "\r\nTo: ann@example.test\r\nSubject: Re: quarterly report\r\n" +
		"In-Reply-To: <" + report.MessageID + ">\r\nMessage-ID: <reply-1@gmail.test>\r\n\r\nThanks!\r\n")
	draftID, _, err := g.Append(ctx, mbs[`\Draft`].ID, raw, []string{"$draft"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.(upstream.MessageSender).SendMessage(ctx, raw, upstream.SendOptions{DraftEmailID: draftID, InReplyToEmailID: report.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.EmailID != draftID {
		t.Fatalf("sent email id %s, want the draft's %s", res.EmailID, draftID)
	}
	sent := e.api.SentMessages()
	if len(sent) != 1 || sent[0].ThreadID != gmHexOf(t, report.UpstreamThreadID) {
		t.Fatalf("sent %+v, want threadId of the report %s", sent, gmHexOf(t, report.UpstreamThreadID))
	}
	wantPlaces(t, e.placesOf(t, acct, draftID), `\Sent`)
	var em model.IndexEmail
	g.Index().DB().First(&em, "id = ?", draftID)
	if em.Draft || !strings.HasPrefix(em.UpstreamID, "gm:") || em.ThreadID != res.ThreadID {
		t.Fatalf("sent email row: draft %v, key %q, thread %s/%s", em.Draft, em.UpstreamID, em.ThreadID, res.ThreadID)
	}
	if e.mail.ByGmMsgID(gmID(t, em.UpstreamID)) == nil {
		t.Fatal("the email does not point at the sent message")
	}
	e.sync(t, acct)
	var n int64
	g.Index().DB().Model(&model.IndexEmail{}).Where("subject = ?", "Re: quarterly report").Count(&n)
	if n != 1 {
		t.Fatalf("after sync, %d emails for the sent message (draft deleted, sent indexed once)", n)
	}

	// A failed send is a classified error.
	e.api.FailNextSend(429, "rateLimitExceeded")
	if _, err := g.(upstream.MessageSender).SendMessage(ctx, raw, upstream.SendOptions{}); !upstream.IsKind(err, upstream.KindThrottled) {
		t.Fatalf("rate-limited send: %v", err)
	}

	// Identities from sendAs: verified ones only, never deletable.
	e.api.SetSendAs(
		gmailtest.SendAs{SendAsEmail: gmailAccount, DisplayName: "Me", IsPrimary: true, IsDefault: true},
		gmailtest.SendAs{SendAsEmail: "Alias@Example.test", DisplayName: "Alias", ReplyToAddress: "r@example.test", Signature: "<b>sig</b>", VerificationStatus: "accepted"},
		gmailtest.SendAs{SendAsEmail: "pending@example.test", VerificationStatus: "pending"},
	)
	list, err := g.(upstream.IdentitySource).Identities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].Email != "alias@example.test" || list[1].ReplyTo != "r@example.test" || list[1].HTMLSignature != "<b>sig</b>" || list[0].MayDelete || list[1].MayDelete {
		t.Fatalf("identities: %+v", list)
	}
	if list[0].ID == list[1].ID || !strings.HasPrefix(list[0].ID, "i") {
		t.Fatalf("identity ids: %s %s", list[0].ID, list[1].ID)
	}
	// Cached: a change within six hours is not seen.
	e.api.SetSendAs(gmailtest.SendAs{SendAsEmail: gmailAccount, IsPrimary: true})
	if again, _ := g.(upstream.IdentitySource).Identities(ctx); len(again) != 2 {
		t.Fatalf("identities not cached: %+v", again)
	}
}

func gmID(t *testing.T, key string) uint64 {
	t.Helper()
	var n uint64
	for _, c := range strings.TrimPrefix(key, "gm:") {
		if c < '0' || c > '9' {
			t.Fatalf("bad key %q", key)
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}

func gmHexOf(t *testing.T, key string) string {
	const digits = "0123456789abcdef"
	n := gmID(t, key)
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%16]}, b...)
		n /= 16
	}
	return string(b)
}
