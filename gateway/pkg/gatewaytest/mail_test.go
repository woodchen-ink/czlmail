package gatewaytest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"
	imapv2 "github.com/emersion/go-imap/v2"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
)

// The properties czlmail's syncer asks for (desktop/internal/syncer/mail.go, body.go).
var (
	czlmailMetaProperties = []string{"id", "blobId", "threadId", "mailboxIds", "keywords", "size",
		"receivedAt", "sentAt", "from", "to", "cc", "bcc", "replyTo",
		"subject", "preview", "hasAttachment", "messageId", "inReplyTo", "bodyStructure"}
	czlmailBodyProperties = []string{"type", "disposition", "name"}
)

var pdfBytes = []byte("%PDF-1.4\n% fake report for the gateway test\n" + strings.Repeat("0123456789", 40) + "\n%%EOF\n")

// multipartSample is text + html alternatives and a PDF attachment.
func multipartSample(subject string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: Alice <alice@example.test>\r\nTo: bob@example.test\r\nSubject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\nMessage-ID: <%d.mp@imaptest.invalid>\r\nList-Unsubscribe: <https://example.test/unsub>\r\n", time.Now().UTC().Format(time.RFC1123Z), time.Now().UnixNano())
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n")
	b.WriteString("--outer\r\nContent-Type: multipart/alternative; boundary=\"inner\"\r\n\r\n")
	b.WriteString("--inner\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString("Hello Bob, the quarterly numbers are attached. Caf=C3=A9 tomorrow?\r\n")
	b.WriteString("--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Hello Bob, the <b>quarterly</b> numbers are attached.</p>\r\n")
	b.WriteString("--inner--\r\n")
	b.WriteString("--outer\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\nContent-Disposition: attachment; filename=\"report.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString(pdfBytes)
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n--outer--\r\n")
	return b.Bytes()
}

// mailEnv is one gateway user with a Dovecot upstream.
type mailEnv struct {
	t      *testing.T
	gw     *Gateway
	cred   Credentials
	mbox   *imaptest.User
	box    *imaptest.Mailbox
	acct   jmap.ID
	c      *jmap.Client
	ids    map[mailbox.Role]jmap.ID
	byName map[string]jmap.ID
}

func newMailEnv(t *testing.T, seed func(box *imaptest.Mailbox)) *mailEnv {
	mbox := imaptest.Dovecot(t).NewUser(t)
	box := mbox.Dial(t)
	if seed != nil {
		seed(box)
	}
	gw := Start(t)
	cred := gw.NewUser(t)
	acct := gw.AddIMAPUpstream(t, cred, mbox)
	e := &mailEnv{t: t, gw: gw, cred: cred, mbox: mbox, box: box, acct: acct, c: gw.Client(t, cred)}
	e.loadMailboxes()
	return e
}

func (e *mailEnv) loadMailboxes() []*mailbox.Mailbox {
	e.t.Helper()
	req := &jmap.Request{}
	req.Invoke(&mailbox.Get{Account: e.acct})
	got := call[*mailbox.GetResponse](e.t, e.c, req)
	e.ids, e.byName = map[mailbox.Role]jmap.ID{}, map[string]jmap.ID{}
	for _, m := range got.List {
		if m.Role != "" {
			e.ids[m.Role] = m.ID
		}
		e.byName[m.Name] = m.ID
	}
	return got.List
}

// call runs a one-call request and returns its typed response.
func call[T any](t *testing.T, c *jmap.Client, req *jmap.Request) T {
	t.Helper()
	resp := Do(t, c, req)
	got, ok := resp.Responses[0].Args.(T)
	if !ok {
		t.Fatalf("%s: %#v", resp.Responses[0].Name, resp.Responses[0].Args)
	}
	return got
}

func (e *mailEnv) query(f email.Filter) []jmap.ID {
	e.t.Helper()
	req := &jmap.Request{}
	req.Invoke(&email.Query{Account: e.acct, Filter: f, Sort: []*email.SortComparator{{Property: "receivedAt", IsAscending: false}}, Limit: 100})
	return call[*email.QueryResponse](e.t, e.c, req).IDs
}

func (e *mailEnv) get(ids []jmap.ID, props []string) map[jmap.ID]*email.Email {
	e.t.Helper()
	req := &jmap.Request{}
	req.Invoke(&email.Get{Account: e.acct, IDs: ids, Properties: props, BodyProperties: czlmailBodyProperties})
	out := map[jmap.ID]*email.Email{}
	for _, m := range call[*email.GetResponse](e.t, e.c, req).List {
		out[m.ID] = m
	}
	return out
}

func (e *mailEnv) state() string {
	e.t.Helper()
	req := &jmap.Request{}
	req.Invoke(&email.Get{Account: e.acct, IDs: []jmap.ID{}, Properties: []string{"id"}})
	return call[*email.GetResponse](e.t, e.c, req).State
}

func (e *mailEnv) changes(since string) *email.ChangesResponse {
	e.t.Helper()
	req := &jmap.Request{}
	req.Invoke(&email.Changes{Account: e.acct, SinceState: since})
	return call[*email.ChangesResponse](e.t, e.c, req)
}

// waitChanges polls Email/changes since state until ok accepts the result.
func (e *mailEnv) waitChanges(since string, ok func(*email.ChangesResponse) bool) *email.ChangesResponse {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		ch := e.changes(since)
		if ok(ch) {
			return ch
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("changes since %s: %+v", since, ch)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (e *mailEnv) set(s *email.Set) *email.SetResponse {
	e.t.Helper()
	s.Account = e.acct
	req := &jmap.Request{}
	req.Invoke(s)
	return call[*email.SetResponse](e.t, e.c, req)
}

func (e *mailEnv) download(blobID jmap.ID) []byte {
	e.t.Helper()
	r, err := e.c.Download(e.acct, blobID)
	if err != nil {
		e.t.Fatalf("download %s: %v", blobID, err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return b
}

// rawCall posts one method call and returns the decoded [name, args] response.
func (e *mailEnv) rawCall(method string, args map[string]any) (string, map[string]any) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []any{[]any{method, args, "c0"}},
	})
	req, _ := http.NewRequest("POST", e.gw.URL+"/jmap/api", bytes.NewReader(body))
	req.SetBasicAuth(e.cred.Email, e.cred.AppPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil || len(out.MethodResponses) == 0 {
		e.t.Fatalf("%s: HTTP %d %s", method, resp.StatusCode, raw)
	}
	var name string
	var res map[string]any
	_ = json.Unmarshal(out.MethodResponses[0][0], &name)
	_ = json.Unmarshal(out.MethodResponses[0][1], &res)
	return name, res
}

func TestMailDovecot(t *testing.T) {
	var plainUID, richUID imapv2.UID
	e := newMailEnv(t, func(box *imaptest.Mailbox) {
		plainUID = box.AppendSample("INBOX", "Plain hello")
		richUID = box.Append("INBOX", multipartSample("Quarterly report"))
	})
	inbox, trash := e.ids[mailbox.RoleInbox], e.ids[mailbox.RoleTrash]
	if inbox == "" || trash == "" || e.ids[mailbox.RoleSent] == "" || e.ids[mailbox.RoleDrafts] == "" {
		t.Fatalf("roles %v", e.ids)
	}
	for _, m := range e.loadMailboxes() {
		if m.ID == inbox && (m.TotalEmails != 2 || m.UnreadEmails != 2 || m.TotalThreads != 2 || m.Rights == nil || !m.Rights.MayReadItems) {
			t.Fatalf("inbox %+v rights %+v", m, m.Rights)
		}
	}

	// czlmail's bootstrap: Email/query by receivedAt, then the list properties.
	ids := e.query(&email.FilterCondition{InMailbox: inbox})
	if len(ids) != 2 {
		t.Fatalf("query inbox: %v", ids)
	}
	got := e.get(ids, czlmailMetaProperties)
	var plain, rich *email.Email
	for _, m := range got {
		switch m.Subject {
		case "Plain hello":
			plain = m
		case "Quarterly report":
			rich = m
		}
	}
	if plain == nil || rich == nil {
		t.Fatalf("emails %+v", got)
	}
	if !rich.HasAttachment || rich.From[0].Email != "alice@example.test" || rich.From[0].Name != "Alice" || !rich.MailboxIDs[inbox] {
		t.Fatalf("rich %+v", rich)
	}
	bs := rich.BodyStructure
	if bs == nil || bs.Type != "multipart/mixed" || len(bs.SubParts) != 2 || bs.SubParts[1].Name != "report.pdf" || bs.SubParts[1].PartID != "" {
		t.Fatalf("bodyStructure %+v", bs)
	}

	// czlmail's body fetch.
	req := &jmap.Request{}
	req.Invoke(&email.Get{Account: e.acct, IDs: []jmap.ID{rich.ID, plain.ID},
		Properties:          []string{"id", "bodyStructure", "textBody", "htmlBody", "bodyValues", "attachments", "headers"},
		FetchTextBodyValues: true, FetchHTMLBodyValues: true, MaxBodyValueBytes: 1 << 20})
	bodies := map[jmap.ID]*email.Email{}
	for _, m := range call[*email.GetResponse](t, e.c, req).List {
		bodies[m.ID] = m
	}
	rb := bodies[rich.ID]
	if len(rb.TextBody) != 1 || rb.TextBody[0].Type != "text/plain" || len(rb.HTMLBody) != 1 || rb.HTMLBody[0].Type != "text/html" {
		t.Fatalf("text %+v html %+v", rb.TextBody, rb.HTMLBody)
	}
	if v := rb.BodyValues[rb.TextBody[0].PartID]; v == nil || !strings.Contains(v.Value, "Café tomorrow") {
		t.Fatalf("text value %+v", v)
	}
	if v := rb.BodyValues[rb.HTMLBody[0].PartID]; v == nil || !strings.Contains(v.Value, "<b>quarterly</b>") {
		t.Fatalf("html value %+v", v)
	}
	if len(rb.Attachments) != 1 || rb.Attachments[0].Name != "report.pdf" || rb.Attachments[0].Type != "application/pdf" {
		t.Fatalf("attachments %+v", rb.Attachments)
	}
	if !slices.ContainsFunc(rb.Headers, func(h *email.Header) bool {
		return h.Name == "List-Unsubscribe" && strings.Contains(h.Value, "unsub")
	}) {
		t.Fatalf("headers %+v", rb.Headers)
	}
	if pb := bodies[plain.ID]; len(pb.TextBody) != 1 || !strings.Contains(pb.BodyValues[pb.TextBody[0].PartID].Value, "Body of Plain hello") {
		t.Fatalf("plain body %+v", pb)
	}
	// Blob downloads: the attachment decoded, the whole message as stored.
	if b := e.download(rb.Attachments[0].BlobID); !bytes.Equal(b, pdfBytes) {
		t.Fatalf("attachment %d bytes, want %d", len(b), len(pdfBytes))
	}
	if b := e.download(rich.BlobID); !bytes.Contains(b, []byte("Subject: Quarterly report")) {
		t.Fatalf("raw message %q", b)
	}

	// Server-side search for what the index cannot answer.
	if ids := e.query(&email.FilterCondition{Body: "quarterly"}); !slices.Equal(ids, []jmap.ID{rich.ID}) {
		t.Fatalf("body search %v", ids)
	}
	if ids := e.query(&email.FilterCondition{InMailbox: inbox, Text: "Body of Plain"}); !slices.Equal(ids, []jmap.ID{plain.ID}) {
		t.Fatalf("text search %v", ids)
	}
	if ids := e.query(&email.FilterCondition{From: "alice"}); !slices.Equal(ids, []jmap.ID{rich.ID}) {
		t.Fatalf("from filter %v", ids)
	}
	hourAgo, inAnHour := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if ids := e.query(&email.FilterCondition{InMailbox: inbox, After: &hourAgo, Before: &inAnHour}); len(ids) != 2 {
		t.Fatalf("date window %v", ids)
	}
	if ids := e.query(&email.FilterCondition{Before: &hourAgo}); len(ids) != 0 {
		t.Fatalf("before filter %v", ids)
	}
	if ids := e.query(&email.FilterCondition{NotKeyword: "$seen", HasAttachment: true}); !slices.Equal(ids, []jmap.ID{rich.ID}) {
		t.Fatalf("keyword + attachment filter %v", ids)
	}
	req = &jmap.Request{}
	req.Invoke(&email.Query{Account: e.acct, Filter: &email.FilterCondition{InMailbox: inbox}, CollapseThreads: true, CalculateTotal: true,
		Sort: []*email.SortComparator{{Property: "from", IsAscending: true}}, Position: 1})
	if q := call[*email.QueryResponse](t, e.c, req); q.Total != 2 || q.Position != 1 || len(q.IDs) != 1 || q.CanCalculateChanges {
		t.Fatalf("collapsed page %+v", q)
	}

	// Another client changes flags, moves, delivers and deletes.
	s0 := e.state()
	e.box.AddFlags("INBOX", plainUID, imapv2.FlagSeen)
	e.box.Move("INBOX", richUID, "Trash")
	e.box.AppendSample("INBOX", "Fresh one")
	ch := e.waitChanges(s0, func(c *email.ChangesResponse) bool { return len(c.Created) == 1 && len(c.Updated) == 2 })
	if len(ch.Destroyed) != 0 {
		t.Fatalf("external move destroyed %v", ch.Destroyed)
	}
	fresh := ch.Created[0]
	after := e.get([]jmap.ID{plain.ID, rich.ID}, []string{"id", "keywords", "mailboxIds"})
	if !after[plain.ID].Keywords["$seen"] || !after[rich.ID].MailboxIDs[trash] {
		t.Fatalf("after external changes %+v %+v", after[plain.ID], after[rich.ID])
	}

	// czlmail's writes: mark read, move to trash, destroy.
	s1 := e.state()
	res := e.set(&email.Set{Update: map[jmap.ID]jmap.Patch{
		fresh:    {"keywords/$seen": true, "keywords/$flagged": true},
		plain.ID: {"mailboxIds": map[string]bool{string(trash): true}},
	}})
	if len(res.Updated) != 2 || len(res.NotUpdated) != 0 || res.NewState == s1 {
		t.Fatalf("set update %+v %+v", res, res.NotUpdated)
	}
	check := e.mbox.Dial(t) // a fresh session: the old one may still see its stale view
	uids := check.UIDs("INBOX")
	if len(uids) != 1 || !slices.Contains(check.Flags("INBOX", uids[0]), imapv2.FlagSeen) {
		t.Fatalf("inbox on the server: %v", uids)
	}
	res = e.set(&email.Set{Destroy: []jmap.ID{rich.ID, "eunknown"}})
	if !slices.Equal(res.Destroyed, []jmap.ID{rich.ID}) || res.NotDestroyed["eunknown"] == nil || res.NotDestroyed["eunknown"].Type != "notFound" {
		t.Fatalf("destroy %+v %+v", res.Destroyed, res.NotDestroyed)
	}
	if n := len(e.mbox.Dial(t).UIDs("Trash")); n != 1 {
		t.Fatalf("trash on the server has %d", n)
	}
	ch = e.changes(s1)
	if !slices.Contains(ch.Destroyed, rich.ID) || !slices.Contains(ch.Updated, plain.ID) || !slices.Contains(ch.Updated, fresh) {
		t.Fatalf("changes after writes %+v", ch)
	}
	// Bad patches are per-email errors.
	res = e.set(&email.Set{Update: map[jmap.ID]jmap.Patch{
		fresh:    {"subject": "no"},
		plain.ID: {"mailboxIds": map[string]bool{string(inbox): true, string(trash): true}},
	}})
	if res.NotUpdated[fresh] == nil || res.NotUpdated[fresh].Type != "invalidProperties" || res.NotUpdated[plain.ID] == nil || res.NotUpdated[plain.ID].Type != "tooManyMailboxes" {
		t.Fatalf("bad patches %+v", res.NotUpdated)
	}
	// ifInState.
	req = &jmap.Request{}
	req.Invoke(&email.Set{Account: e.acct, IfInState: "1-0", Destroy: []jmap.ID{fresh}})
	if me, ok := Do(t, e.c, req).Responses[0].Args.(*jmap.MethodError); !ok || me.Type != "stateMismatch" {
		t.Fatalf("ifInState: %#v", Do(t, e.c, req).Responses[0].Args)
	}

	// A folder rebuilt with a new UIDVALIDITY: changes stay calculable.
	e.box.Create("Projects")
	e.box.AppendSample("Projects", "Project plan")
	s2 := e.state()
	e.waitChanges(s2, func(c *email.ChangesResponse) bool { return len(c.Created) == 1 }) // wait for the import
	e.loadMailboxes()
	projects := e.byName["Projects"]
	planIDs := e.query(&email.FilterCondition{InMailbox: projects})
	if projects == "" || len(planIDs) != 1 {
		t.Fatalf("projects %q %v", projects, planIDs)
	}
	s3 := e.state()
	e.box.Recreate("Projects")
	ch = e.waitChanges(s3, func(c *email.ChangesResponse) bool { return slices.Contains(c.Destroyed, planIDs[0]) })
	if ch.NewState == s3 {
		t.Fatalf("no new state after the rebuild")
	}

	// Wrong password upstream: reads come from the index, writes fail with 40110.
	bad := IMAPUpstreamBody(e.mbox)
	bad["imap"].(map[string]any)["password"] = "wrong"
	e.gw.UserAdmin(t, e.cred).Do(t, "PATCH", "/api/admin/upstreams/"+string(e.acct), map[string]any{"imap": bad["imap"]}, nil)
	e.gw.WaitUpstream(t, e.cred, string(e.acct), "authRequired", 30*time.Second)
	if ids := e.query(&email.FilterCondition{InMailbox: inbox}); !slices.Equal(ids, []jmap.ID{fresh}) {
		t.Fatalf("index read while auth failed: %v", ids)
	}
	if m := e.get([]jmap.ID{fresh}, czlmailMetaProperties)[fresh]; m == nil || m.Subject != "Fresh one" {
		t.Fatalf("get while auth failed: %+v", m)
	}
	name, args := e.rawCall("Email/set", map[string]any{"accountId": e.acct, "update": map[string]any{string(fresh): map[string]any{"keywords/$seen": nil}}})
	up, _ := args["czlUpstream"].(map[string]any)
	if name != "error" || args["type"] != "serverUnavailable" || up["code"] != float64(40110) || up["status"] != "authRequired" {
		t.Fatalf("write while auth failed: %s %v", name, args)
	}
	name, args = e.rawCall("Email/get", map[string]any{"accountId": e.acct, "ids": []string{string(fresh)}, "properties": []string{"bodyValues"}, "fetchTextBodyValues": true})
	if name != "error" || args["type"] != "serverUnavailable" {
		t.Fatalf("body while auth failed: %s %v", name, args)
	}
}

func TestMailDovecotMailboxesAndImport(t *testing.T) {
	e := newMailEnv(t, nil)
	inbox := e.ids[mailbox.RoleInbox]

	// Create a parent and a child in one call, then rename, move and destroy.
	req := &jmap.Request{}
	req.Invoke(&mailbox.Set{Account: e.acct, Create: map[jmap.ID]*mailbox.Mailbox{
		"p": {Name: "Clients"},
		"c": {Name: "Acme", ParentID: "#p"},
	}})
	res := call[*mailbox.SetResponse](t, e.c, req)
	if len(res.Created) != 2 || res.NotCreated != nil && len(res.NotCreated) > 0 {
		t.Fatalf("create %+v %+v", res.Created, res.NotCreated)
	}
	parent, child := res.Created["p"].ID, res.Created["c"].ID
	req = &jmap.Request{}
	req.Invoke(&mailbox.Set{Account: e.acct, Update: map[jmap.ID]jmap.Patch{child: {"name": "Acme Corp", "parentId": nil}}})
	if r := call[*mailbox.SetResponse](t, e.c, req); len(r.Updated) != 1 {
		t.Fatalf("rename %+v", r.NotUpdated)
	}
	all := e.loadMailboxes()
	if e.byName["Acme Corp"] != child {
		t.Fatalf("renamed mailbox lost its id: %v", e.byName)
	}
	_ = all

	// Import a message into the child, then destroying it needs onDestroyRemoveEmails.
	up, err := e.c.Upload(e.acct, bytes.NewReader(imaptest.Sample("Imported note")))
	if err != nil {
		t.Fatal(err)
	}
	req = &jmap.Request{}
	req.Invoke(&email.Import{Account: e.acct, Emails: map[string]*email.EmailImport{
		"n": {BlobID: up.ID, MailboxIDs: map[jmap.ID]bool{child: true}, Keywords: map[string]bool{"$seen": true}},
	}})
	imp := call[*email.ImportResponse](t, e.c, req)
	created := imp.Created["n"]
	if created == nil || created.ID == "" || created.ThreadID == "" {
		t.Fatalf("import %+v %+v", imp.Created, imp.NotCreated)
	}
	if uids := e.box.UIDs("Acme Corp"); len(uids) != 1 || !slices.Contains(e.box.Flags("Acme Corp", uids[0]), imapv2.FlagSeen) {
		t.Fatalf("imported on the server: %v", uids)
	}
	if m := e.get([]jmap.ID{created.ID}, []string{"id", "subject", "mailboxIds"})[created.ID]; m == nil || m.Subject != "Imported note" {
		t.Fatalf("imported email %+v", m)
	}

	req = &jmap.Request{}
	req.Invoke(&mailbox.Set{Account: e.acct, Destroy: []jmap.ID{child, inbox}})
	r := call[*mailbox.SetResponse](t, e.c, req)
	if r.NotDestroyed[child] == nil || r.NotDestroyed[child].Type != "mailboxHasEmail" || r.NotDestroyed[inbox] == nil {
		t.Fatalf("destroy refusals %+v", r.NotDestroyed)
	}
	name, args := e.rawCall("Mailbox/set", map[string]any{"accountId": e.acct, "destroy": []string{string(child), string(parent)}, "onDestroyRemoveEmails": true})
	if name != "Mailbox/set" || len(args["destroyed"].([]any)) != 2 {
		t.Fatalf("destroy %s %v", name, args)
	}

	// Email/set create builds the message from its properties.
	name, args = e.rawCall("Email/set", map[string]any{"accountId": e.acct, "create": map[string]any{"d": map[string]any{
		"mailboxIds": map[string]bool{string(e.ids[mailbox.RoleDrafts]): true},
		"keywords":   map[string]bool{"$draft": true},
		"from":       []map[string]string{{"name": "Me", "email": "me@example.test"}},
		"to":         []map[string]string{{"email": "you@example.test"}},
		"subject":    "Draft über",
		"textBody":   []map[string]string{{"partId": "t", "type": "text/plain"}},
		"bodyValues": map[string]any{"t": map[string]string{"value": "Draft text\n"}},
	}}})
	createdD, _ := args["created"].(map[string]any)["d"].(map[string]any)
	if name != "Email/set" || createdD == nil {
		t.Fatalf("create %s %v", name, args)
	}
	id := jmap.ID(createdD["id"].(string))
	req = &jmap.Request{}
	req.Invoke(&email.Get{Account: e.acct, IDs: []jmap.ID{id}, Properties: []string{"subject", "textBody", "bodyValues"}, FetchTextBodyValues: true})
	m := call[*email.GetResponse](t, e.c, req).List[0]
	if m.Subject != "Draft über" || !strings.Contains(m.BodyValues[m.TextBody[0].PartID].Value, "Draft text") {
		t.Fatalf("created draft %+v", m)
	}
	// queryChanges is never calculable.
	name, args = e.rawCall("Email/queryChanges", map[string]any{"accountId": e.acct, "sinceQueryState": "x"})
	if name != "error" || args["type"] != "cannotCalculateChanges" {
		t.Fatalf("queryChanges %s %v", name, args)
	}
}
