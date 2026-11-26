package submission_test

import (
	"slices"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"git.sr.ht/~rockorager/go-jmap/mail/emailsubmission"

	"github.com/woodchen-ink/czlmail/gateway/pkg/gatewaytest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
)

// The czlmail send flow (desktop/internal/syncer/compose.go) in one request:
// Email/set creates the draft, EmailSubmission/set sends it by back-reference and
// onSuccessUpdateEmail moves it to Sent; Email/changes then shows the same id
// updated, not a new email.
func TestCzlmailSendFlow(t *testing.T) {
	h := newHarness(t, harnessOptions{sentCopy: "gateway"})
	mp := smtptest.New(t)
	c := h.gw.Client(t, h.cred)
	acct := jmap.ID(h.acct)
	drafts, sent := jmap.ID(h.mailboxID("drafts")), jmap.ID(h.mailboxID("sent"))

	pre := &jmap.Request{}
	pre.Invoke(&email.Get{Account: acct, IDs: []jmap.ID{}})
	resp := gatewaytest.Do(t, c, pre)
	before, ok := resp.Responses[0].Args.(*email.GetResponse)
	if !ok {
		if me, isErr := resp.Responses[0].Args.(*jmap.MethodError); isErr && me.Type == "unknownMethod" {
			t.Skip("Email/set and Email/changes come with #40")
		}
		t.Fatalf("Email/get: %#v", resp.Responses[0].Args)
	}

	to := smtptest.UniqueAddress("flow")
	text := "hello from the czlmail flow"
	req := &jmap.Request{}
	req.Invoke(&email.Set{Account: acct, Create: map[jmap.ID]*email.Email{"draft": {
		MailboxIDs: map[jmap.ID]bool{drafts: true},
		Keywords:   map[string]bool{"$draft": true, "$seen": true},
		From:       []*mail.Address{{Name: "Sender", Email: h.email}},
		To:         []*mail.Address{{Email: to}},
		Subject:    "czlmail flow",
		BodyValues: map[string]*email.BodyValue{"t": {Value: text}},
		TextBody:   []*email.BodyPart{{PartID: "t", Type: "text/plain"}},
	}}})
	req.Invoke(&emailsubmission.Set{
		Account: acct,
		Create: map[jmap.ID]*emailsubmission.EmailSubmission{"sub": {
			IdentityID: jmap.ID(h.identityID()), EmailID: "#draft",
		}},
		OnSuccessUpdateEmail: map[jmap.ID]jmap.Patch{"#sub": {
			"keywords/$draft": nil, "mailboxIds/" + string(drafts): nil, "mailboxIds/" + string(sent): true,
		}},
	})
	resp = gatewaytest.Do(t, c, req)
	created, ok := resp.Responses[0].Args.(*email.SetResponse)
	if !ok || created.Created["draft"] == nil {
		t.Fatalf("Email/set: %#v", resp.Responses[0].Args)
	}
	draftID := created.Created["draft"].ID
	if sub, ok := resp.Responses[1].Args.(*emailsubmission.SetResponse); !ok || sub.Created["sub"] == nil {
		t.Fatalf("EmailSubmission/set: %#v", resp.Responses[1].Args)
	}
	if len(resp.Responses) < 3 {
		t.Fatalf("no implicit Email/set: %d responses", len(resp.Responses))
	}
	if up, ok := resp.Responses[2].Args.(*email.SetResponse); !ok || up.Updated == nil {
		t.Fatalf("implicit Email/set: %#v", resp.Responses[2].Args)
	} else if _, ok := up.Updated[draftID]; !ok {
		t.Fatalf("implicit Email/set did not update %s: %#v", draftID, up)
	}
	mp.WaitFor("to:"+to, 1, 10*time.Second)

	ch := &jmap.Request{}
	ch.Invoke(&email.Changes{Account: acct, SinceState: before.State})
	ch.Invoke(&email.Get{Account: acct, IDs: []jmap.ID{draftID}, Properties: []string{"mailboxIds", "keywords"}})
	resp = gatewaytest.Do(t, c, ch)
	changes, ok := resp.Responses[0].Args.(*email.ChangesResponse)
	if !ok {
		t.Fatalf("Email/changes: %#v", resp.Responses[0].Args)
	}
	all := slices.Concat(changes.Created, changes.Updated)
	if !slices.Contains(all, draftID) || len(changes.Created) > 1 || slices.Contains(changes.Destroyed, draftID) {
		t.Fatalf("Email/changes %+v: want only %s", changes, draftID)
	}
	got := resp.Responses[1].Args.(*email.GetResponse)
	if len(got.List) != 1 || !got.List[0].MailboxIDs[sent] || len(got.List[0].MailboxIDs) != 1 || got.List[0].Keywords["$draft"] {
		t.Fatalf("after sending: %+v", got.List)
	}
}
