package submission_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"

	"github.com/woodchen-ink/czlmail/gateway/initapp"
	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/gatewaytest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
	"github.com/woodchen-ink/czlmail/gateway/service/submission"
)

const (
	coreURI       = "urn:ietf:params:jmap:core"
	mailURI       = "urn:ietf:params:jmap:mail"
	submissionURI = "urn:ietf:params:jmap:submission"
)

// harness is a gateway user with one Dovecot mailbox whose SMTP is Mailpit or a fake.
type harness struct {
	t     *testing.T
	gw    *gatewaytest.Gateway
	cred  gatewaytest.Credentials
	user  *imaptest.User
	box   *imaptest.Mailbox
	acct  string
	email string // the upstream's address
}

type harnessOptions struct {
	smtp     *smtptest.Fake // nil: Mailpit
	sentCopy string
	wait     time.Duration
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	dov := imaptest.Dovecot(t)
	if o.smtp == nil {
		smtptest.New(t) // skips without Mailpit
	}
	if o.wait == 0 {
		o.wait = 2 * time.Second
	}
	gw := gatewaytest.StartWith(t, initapp.Options{Submission: submission.Options{
		SentCopyWait: o.wait, RetryDelays: []time.Duration{20 * time.Millisecond, 20 * time.Millisecond},
	}})
	cred := gw.NewUser(t)
	user := dov.NewUser(t)
	box := user.Dial(t)
	gw.AllowPrivateNetworks(t)
	body := gatewaytest.IMAPUpstreamBody(user)
	if o.smtp != nil {
		host, port, _ := net.SplitHostPort(o.smtp.Addr)
		p, _ := strconv.Atoi(port)
		body["smtp"] = map[string]any{"host": host, "port": p, "tls": "plain", "username": user.Name, "password": "any"}
	}
	if o.sentCopy != "" {
		body["sentCopy"] = o.sentCopy
	}
	acct := gw.CreateUpstream(t, cred, body)
	gw.WaitUpstream(t, cred, acct, "ok", 30*time.Second)
	return &harness{t: t, gw: gw, cred: cred, user: user, box: box, acct: acct, email: body["email"].(string)}
}

// api runs one JMAP request of raw invocations and returns the responses.
func (h *harness) api(calls ...[]any) [][]any {
	h.t.Helper()
	body, _ := json.Marshal(map[string]any{"using": []string{coreURI, mailURI, submissionURI}, "methodCalls": calls})
	req, _ := http.NewRequest("POST", h.gw.URL+"/jmap/api", bytes.NewReader(body))
	req.SetBasicAuth(h.cred.Email, h.cred.AppPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		MethodResponses [][]any `json:"methodResponses"`
	}
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		h.t.Fatalf("jmap api: %s %s", resp.Status, b.String())
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.t.Fatal(err)
	}
	return out.MethodResponses
}

// args returns the arguments of a response, failing on an error response.
func args(t *testing.T, r []any, name string) map[string]any {
	t.Helper()
	if r[0] != name {
		t.Fatalf("response %v, want %s", r, name)
	}
	return r[1].(map[string]any)
}

func (h *harness) identityID() string {
	h.t.Helper()
	r := h.api([]any{"Identity/get", map[string]any{"accountId": h.acct}, "0"})
	list := args(h.t, r[0], "Identity/get")["list"].([]any)
	for _, x := range list {
		m := x.(map[string]any)
		if strings.EqualFold(m["email"].(string), h.email) {
			return m["id"].(string)
		}
	}
	h.t.Fatalf("no identity for %s in %v", h.email, list)
	return ""
}

// draft builds a message from the upstream's address.
func (h *harness) draft(msgID, to, cc, bcc string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: Sender <%s>\r\nTo: <%s>\r\n", h.email, to)
	if cc != "" {
		fmt.Fprintf(&b, "Cc: <%s>\r\n", cc)
	}
	if bcc != "" {
		// Folded on purpose: the continuation line must go too.
		fmt.Fprintf(&b, "Bcc: Hidden\r\n <%s>\r\n", bcc)
	}
	fmt.Fprintf(&b, "Subject: test %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n",
		msgID, time.Now().UTC().Format(time.RFC1123Z), msgID)
	return []byte(b.String())
}

// appendDraft stores raw in Drafts and waits until the gateway indexed it.
func (h *harness) appendDraft(raw []byte, msgID string) string {
	h.t.Helper()
	h.box.Append("Drafts", raw, goimap.FlagDraft, goimap.FlagSeen)
	return h.waitEmail(msgID, "drafts", 1)
}

func (h *harness) mailboxID(role string) string {
	h.t.Helper()
	handle, uerr := h.gw.App.Manager.Lookup(h.acct)
	if uerr != nil {
		h.t.Fatal(uerr)
	}
	var m model.IndexMailbox
	if err := handle.Upstream.Index().DB().Where("role = ?", role).First(&m).Error; err != nil {
		h.t.Fatalf("no %s mailbox: %v", role, err)
	}
	return m.ID
}

// emailsByMessageID returns email id → mailbox roles for a Message-ID.
func (h *harness) emailsByMessageID(msgID string) map[string][]string {
	h.t.Helper()
	handle, uerr := h.gw.App.Manager.Lookup(h.acct)
	if uerr != nil {
		h.t.Fatal(uerr)
	}
	var rows []struct {
		ID   string
		Role string
	}
	err := handle.Upstream.Index().DB().Table("emails e").Select("e.id, mb.role").
		Joins("LEFT JOIN memberships ms ON ms.email_id = e.id").Joins("LEFT JOIN mailboxes mb ON mb.id = ms.mailbox_id").
		Where("e.message_id = ? AND e.orphaned_from = ''", msgID).Scan(&rows).Error
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string][]string{}
	for _, r := range rows {
		out[r.ID] = append(out[r.ID], r.Role)
	}
	return out
}

// waitEmail waits until exactly n emails with msgID exist and one is in role; it
// returns that one's id.
func (h *harness) waitEmail(msgID, role string, n int) string {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		got := h.emailsByMessageID(msgID)
		if len(got) == n {
			for id, roles := range got {
				if len(roles) == 1 && roles[0] == role {
					return id
				}
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("emails with %s: %v, want %d with one in %s", msgID, got, n, role)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (h *harness) keywords(emailID string) map[string]bool {
	h.t.Helper()
	handle, _ := h.gw.App.Manager.Lookup(h.acct)
	var e model.IndexEmail
	if err := handle.Upstream.Index().DB().Select("keywords").Where("id = ?", emailID).First(&e).Error; err != nil {
		h.t.Fatal(err)
	}
	var kw map[string]bool
	_ = json.Unmarshal([]byte(e.Keywords), &kw)
	return kw
}

// submit sends emailID with the czlmail patch (Drafts → Sent, $draft off).
func (h *harness) submit(emailID string, envelope map[string]any) (created map[string]any, notCreated map[string]any, emailSet map[string]any) {
	h.t.Helper()
	sub := map[string]any{"identityId": h.identityID(), "emailId": emailID}
	if envelope != nil {
		sub["envelope"] = envelope
	}
	drafts, sent := h.mailboxID("drafts"), h.mailboxID("sent")
	r := h.api([]any{"EmailSubmission/set", map[string]any{
		"accountId": h.acct,
		"create":    map[string]any{"sub": sub},
		"onSuccessUpdateEmail": map[string]any{"#sub": map[string]any{
			"keywords/$draft": nil, "mailboxIds/" + drafts: nil, "mailboxIds/" + sent: true,
		}},
	}, "0"})
	a := args(h.t, r[0], "EmailSubmission/set")
	created, _ = a["created"].(map[string]any)
	notCreated, _ = a["notCreated"].(map[string]any)
	if len(r) > 1 {
		emailSet = args(h.t, r[1], "Email/set")
	}
	return created, notCreated, emailSet
}

func (h *harness) upstreamSentCopy() string {
	h.t.Helper()
	var row model.Upstream
	if err := h.gw.DB.Select("sent_copy").Where("public_id = ?", h.acct).First(&row).Error; err != nil {
		h.t.Fatal(err)
	}
	return row.SentCopy
}

func uniqueID(tag string) string {
	return tag + "." + strconv.FormatInt(time.Now().UnixNano(), 36) + "@sender.test"
}
