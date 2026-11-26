package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// readBlob reads an uploaded or stored blob of the account, at most limit bytes.
func (s *Service) readBlob(ctx context.Context, call *jmapserver.Call, a *account, blobID string, limit int64) ([]byte, string, error) {
	if blobID != "" && blobID[0] == jmapserver.UploadPrefix {
		if s.opts.Uploads == nil {
			return nil, "", jmapserver.ErrBlobNotFound
		}
		f, meta, err := s.opts.Uploads.Open(call.UserID, a.id, blobID)
		if err != nil {
			return nil, "", err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, limit+1))
		return b, meta.Type, err
	}
	r, _, err := s.openBlob(ctx, a.id, blobID)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	return b, "", err
}

var errBlobNotFound = setErr("blobNotFound", "")

// blobFailure maps a readBlob error: a missing blob is a SetError, an upstream
// outage fails the call.
func blobFailure(err error, prop string) (*SetError, error) {
	if errors.Is(err, jmapserver.ErrBlobNotFound) {
		return &SetError{Type: "blobNotFound", Properties: []string{prop}}, nil
	}
	if ue, ok := errors.AsType[*jmapserver.UpstreamError](err); ok {
		return nil, jmapserver.ServerUnavailable(ue)
	}
	return nil, err
}

// store appends a message and reports the created Email.
func (s *Service) store(ctx context.Context, call *jmapserver.Call, a *account, resp *setResponse, cid string,
	mailboxID string, raw []byte, keywords []string, receivedAt time.Time) error {
	id, _, err := a.up.Append(ctx, mailboxID, raw, keywords, receivedAt)
	if err != nil {
		se, ferr := resp.upstreamFailure(err, setErr("invalidEmail", "the mail server refused the message"))
		if ferr != nil {
			return ferr
		}
		resp.notCreated(cid, se)
		return nil
	}
	if id == "" {
		resp.notCreated(cid, setErr("serverFail", "the message was stored but could not be found again"))
		return nil
	}
	call.CreatedIDs[cid] = id
	var row struct {
		ThreadID string
		Size     int64
	}
	if err := a.idx.DB().Table("emails").Select("thread_id, size").Where("id = ?", id).Scan(&row).Error; err != nil {
		return err
	}
	resp.created(cid, map[string]any{"id": id, "blobId": blobIDMessage(id), "threadId": row.ThreadID, "size": row.Size})
	return nil
}

// emailImport is Email/import (RFC 8621 §4.8).
func (s *Service) emailImport(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args struct {
		AccountID string  `json:"accountId"`
		IfInState *string `json:"ifInState"`
		Emails    map[string]struct {
			BlobID     string          `json:"blobId"`
			MailboxIDs map[string]bool `json:"mailboxIds"`
			Keywords   map[string]bool `json:"keywords"`
			ReceivedAt *time.Time      `json:"receivedAt"`
		} `json:"emails"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if len(args.Emails) > jmapserver.MaxObjectsInSet {
		return nil, jmapserver.ErrRequestTooLarge
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeEmail)
	if err := checkState(args.IfInState, state); err != nil {
		return nil, err
	}
	resp := newSetResponse(a.id, state)
	if len(args.Emails) > 0 {
		if err := a.online(); err != nil {
			return nil, err
		}
	}
	resolve, err := s.mailboxResolver(call, a)
	if err != nil {
		return nil, err
	}
	cids := make([]string, 0, len(args.Emails))
	for cid := range args.Emails {
		cids = append(cids, cid)
	}
	sort.Strings(cids)
	for _, cid := range cids {
		e := args.Emails[cid]
		mbox, se := singleMailbox(e.MailboxIDs, resolve)
		if se == nil {
			se = checkKeywords(e.Keywords)
		}
		if se != nil {
			resp.notCreated(cid, se)
			continue
		}
		raw, _, err := s.readBlob(ctx, call, a, e.BlobID, jmapserver.MaxSizeUpload)
		if err != nil {
			se, ferr := blobFailure(err, "blobId")
			if ferr != nil {
				return nil, ferr
			}
			resp.notCreated(cid, se)
			continue
		}
		if len(raw) > jmapserver.MaxSizeUpload {
			resp.notCreated(cid, setErr("tooLarge", ""))
			continue
		}
		received := time.Now()
		if e.ReceivedAt != nil {
			received = *e.ReceivedAt
		}
		if err := s.store(ctx, call, a, resp, cid, mbox, raw, keywordList(e.Keywords), received); err != nil {
			return nil, err
		}
	}
	resp.NewState = a.state(mailindex.TypeEmail)
	return struct {
		AccountID  string               `json:"accountId"`
		OldState   string               `json:"oldState"`
		NewState   string               `json:"newState"`
		Created    map[string]any       `json:"created"`
		NotCreated map[string]*SetError `json:"notCreated"`
	}{resp.AccountID, resp.OldState, resp.NewState, resp.Created, resp.NotCreated}, nil
}

func singleMailbox(ids map[string]bool, resolve func(string) (string, bool)) (string, *SetError) {
	var out []string
	for id, on := range ids {
		real, ok := resolve(id)
		if !on || !ok {
			return "", invalidProps("unknown mailbox", "mailboxIds")
		}
		out = append(out, real)
	}
	switch len(out) {
	case 0:
		return "", invalidProps("an email must be in a mailbox", "mailboxIds")
	case 1:
		return out[0], nil
	}
	return "", errTooManyMailboxes
}

func checkKeywords(kw map[string]bool) *SetError {
	for k, on := range kw {
		if !on || !validKeyword(k) {
			return invalidProps("invalid keyword", "keywords")
		}
	}
	return nil
}

func keywordList(kw map[string]bool) []string {
	out := make([]string, 0, len(kw))
	for k := range kw {
		out = append(out, strings.ToLower(k))
	}
	sort.Strings(out)
	return out
}

// Email/set create: the message is built from its JMAP properties.

type createPart struct {
	PartID      *string  `json:"partId"`
	BlobID      *string  `json:"blobId"`
	Type        string   `json:"type"`
	Charset     *string  `json:"charset"`
	Name        *string  `json:"name"`
	Disposition *string  `json:"disposition"`
	CID         *string  `json:"cid"`
	Language    []string `json:"language"`
	Location    *string  `json:"location"`
}

type createEmail struct {
	MailboxIDs  map[string]bool      `json:"mailboxIds"`
	Keywords    map[string]bool      `json:"keywords"`
	ReceivedAt  *time.Time           `json:"receivedAt"`
	MessageID   []string             `json:"messageId"`
	InReplyTo   []string             `json:"inReplyTo"`
	References  []string             `json:"references"`
	Sender      []emailAddress       `json:"sender"`
	From        []emailAddress       `json:"from"`
	To          []emailAddress       `json:"to"`
	Cc          []emailAddress       `json:"cc"`
	Bcc         []emailAddress       `json:"bcc"`
	ReplyTo     []emailAddress       `json:"replyTo"`
	Subject     *string              `json:"subject"`
	SentAt      *time.Time           `json:"sentAt"`
	TextBody    []createPart         `json:"textBody"`
	HTMLBody    []createPart         `json:"htmlBody"`
	Attachments []createPart         `json:"attachments"`
	BodyValues  map[string]bodyValue `json:"bodyValues"`
	headers     []headerField
}

var createSettable = map[string]bool{"mailboxIds": true, "keywords": true, "receivedAt": true, "messageId": true,
	"inReplyTo": true, "references": true, "sender": true, "from": true, "to": true, "cc": true, "bcc": true,
	"replyTo": true, "subject": true, "sentAt": true, "textBody": true, "htmlBody": true, "attachments": true,
	"bodyValues": true}

func (s *Service) createEmail(ctx context.Context, call *jmapserver.Call, a *account, resp *setResponse, cid string,
	props map[string]json.RawMessage) error {
	var e createEmail
	for k, raw := range props {
		switch {
		case createSettable[k]:
		case strings.HasPrefix(k, "header:"):
			hp, ok := parseHeaderProp(k)
			if !ok || hp.all || (hp.form != "Raw" && hp.form != "Text") || strings.HasPrefix(hp.name, "content-") {
				resp.notCreated(cid, invalidProps("unsupported header property", k))
				return nil
			}
			var v string
			if json.Unmarshal(raw, &v) != nil || strings.ContainsAny(v, "\r\n") && hp.form == "Text" {
				resp.notCreated(cid, invalidProps("invalid value", k))
				return nil
			}
			name := strings.SplitN(strings.TrimPrefix(k, "header:"), ":", 2)[0]
			if hp.form == "Text" {
				v = " " + mime.QEncoding.Encode("utf-8", v)
			}
			e.headers = append(e.headers, headerField{name: name, value: v})
			continue
		case k == "bodyStructure":
			resp.notCreated(cid, invalidProps("use textBody, htmlBody and attachments", k))
			return nil
		default:
			resp.notCreated(cid, invalidProps("unknown or server-set property", k))
			return nil
		}
	}
	b, _ := json.Marshal(props)
	if err := json.Unmarshal(b, &e); err != nil {
		resp.notCreated(cid, invalidProps(err.Error()))
		return nil
	}
	resolve, err := s.mailboxResolver(call, a)
	if err != nil {
		return err
	}
	mbox, se := singleMailbox(e.MailboxIDs, resolve)
	if se == nil {
		se = checkKeywords(e.Keywords)
	}
	if se != nil {
		resp.notCreated(cid, se)
		return nil
	}
	raw, se, err := s.buildMessage(ctx, call, a, &e)
	if err != nil {
		return err
	}
	if se != nil {
		resp.notCreated(cid, se)
		return nil
	}
	received := time.Now()
	if e.ReceivedAt != nil {
		received = *e.ReceivedAt
	}
	return s.store(ctx, call, a, resp, cid, mbox, raw, keywordList(e.Keywords), received)
}

// buildMessage writes the RFC 5322 message of an Email/set create.
func (s *Service) buildMessage(ctx context.Context, call *jmapserver.Call, a *account, e *createEmail) ([]byte, *SetError, error) {
	var head bytes.Buffer
	hdr := func(name, value string) { fmt.Fprintf(&head, "%s: %s\r\n", name, value) }
	date := time.Now()
	if e.SentAt != nil {
		date = *e.SentAt
	}
	hdr("Date", date.Format(time.RFC1123Z))
	for _, f := range []struct {
		name string
		list []emailAddress
	}{{"From", e.From}, {"Sender", e.Sender}, {"Reply-To", e.ReplyTo}, {"To", e.To}, {"Cc", e.Cc}, {"Bcc", e.Bcc}} {
		if len(f.list) > 0 {
			hdr(f.name, formatAddresses(f.list))
		}
	}
	if e.Subject != nil {
		hdr("Subject", mime.QEncoding.Encode("utf-8", *e.Subject))
	}
	msgID := ""
	if len(e.MessageID) > 0 {
		msgID = e.MessageID[0]
	} else {
		msgID = newMessageID(e.From)
	}
	hdr("Message-ID", "<"+msgID+">")
	if len(e.InReplyTo) > 0 {
		hdr("In-Reply-To", angleList(e.InReplyTo))
	}
	if len(e.References) > 0 {
		hdr("References", angleList(e.References))
	}
	for _, h := range e.headers {
		fmt.Fprintf(&head, "%s:%s\r\n", h.name, strings.TrimRight(h.value, "\r\n"))
	}
	hdr("MIME-Version", "1.0")

	text, se := e.textPart(e.TextBody, "text/plain")
	if se != nil {
		return nil, se, nil
	}
	html, se := e.textPart(e.HTMLBody, "text/html")
	if se != nil {
		return nil, se, nil
	}
	var body *mimeNode
	switch {
	case text != nil && html != nil:
		body = &mimeNode{multipart: "alternative", children: []*mimeNode{text, html}}
	case html != nil:
		body = html
	case text != nil:
		body = text
	default:
		body = &mimeNode{header: "Content-Type: text/plain; charset=utf-8\r\n", body: nil}
	}
	var inline, attached []*mimeNode
	var total int64
	for _, at := range e.Attachments {
		if at.BlobID == nil {
			return nil, invalidProps("an attachment needs a blobId", "attachments"), nil
		}
		data, upType, err := s.readBlob(ctx, call, a, *at.BlobID, jmapserver.MaxSizeUpload)
		if err != nil {
			se, ferr := blobFailure(err, "attachments")
			return nil, se, ferr
		}
		total += int64(len(data))
		if total > jmapserver.MaxSizeUpload {
			return nil, setErr("tooLarge", "the attachments exceed maxSizeAttachmentsPerEmail"), nil
		}
		n := attachmentNode(at, data, upType)
		if at.CID != nil && (at.Disposition == nil || *at.Disposition == "inline") {
			inline = append(inline, n)
		} else {
			attached = append(attached, n)
		}
	}
	if len(inline) > 0 {
		body = &mimeNode{multipart: "related", children: append([]*mimeNode{body}, inline...)}
	}
	if len(attached) > 0 {
		body = &mimeNode{multipart: "mixed", children: append([]*mimeNode{body}, attached...)}
	}
	var out bytes.Buffer
	out.Write(head.Bytes())
	body.write(&out)
	return out.Bytes(), nil, nil
}

// textPart builds the body of textBody or htmlBody: one part, its value in bodyValues.
func (e *createEmail) textPart(parts []createPart, want string) (*mimeNode, *SetError) {
	if len(parts) == 0 {
		return nil, nil
	}
	prop := "textBody"
	if want == "text/html" {
		prop = "htmlBody"
	}
	if len(parts) > 1 {
		return nil, invalidProps("only one part is supported", prop)
	}
	p := parts[0]
	if p.PartID == nil || p.BlobID != nil {
		return nil, invalidProps("the part needs a partId with a value in bodyValues", prop)
	}
	if p.Type != "" && !strings.EqualFold(p.Type, want) {
		return nil, invalidProps("the part must be "+want, prop)
	}
	v, ok := e.BodyValues[*p.PartID]
	if !ok || v.IsTruncated || v.IsEncodingProblem {
		return nil, invalidProps("no usable value for the part", "bodyValues")
	}
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	w.Write([]byte(normalizeCRLF(v.Value)))
	w.Close()
	return &mimeNode{header: "Content-Type: " + want + "; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n",
		body: qp.Bytes()}, nil
}

func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func attachmentNode(at createPart, data []byte, uploadType string) *mimeNode {
	typ := at.Type
	if typ == "" {
		typ = uploadType
	}
	if typ == "" {
		typ = "application/octet-stream"
	}
	params := map[string]string{}
	if at.Name != nil && *at.Name != "" {
		params["name"] = *at.Name
	}
	var h strings.Builder
	fmt.Fprintf(&h, "Content-Type: %s\r\n", mime.FormatMediaType(typ, params))
	disp := "attachment"
	if at.Disposition != nil && *at.Disposition != "" {
		disp = *at.Disposition
	}
	dp := map[string]string{}
	if at.Name != nil && *at.Name != "" {
		dp["filename"] = *at.Name
	}
	fmt.Fprintf(&h, "Content-Disposition: %s\r\n", mime.FormatMediaType(disp, dp))
	if at.CID != nil && *at.CID != "" {
		fmt.Fprintf(&h, "Content-ID: <%s>\r\n", strings.Trim(*at.CID, "<>"))
	}
	h.WriteString("Content-Transfer-Encoding: base64\r\n")
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return &mimeNode{header: h.String(), body: b.Bytes()}
}

// mimeNode is a part being written: a leaf with its header, or a multipart.
type mimeNode struct {
	header    string
	body      []byte
	multipart string
	children  []*mimeNode
}

func (n *mimeNode) write(w *bytes.Buffer) {
	if n.multipart == "" {
		w.WriteString(n.header)
		w.WriteString("\r\n")
		w.Write(n.body)
		w.WriteString("\r\n")
		return
	}
	boundary := randomHex(12)
	fmt.Fprintf(w, "Content-Type: multipart/%s; boundary=\"%s\"\r\n\r\n", n.multipart, boundary)
	for _, c := range n.children {
		fmt.Fprintf(w, "--%s\r\n", boundary)
		c.write(w)
	}
	fmt.Fprintf(w, "--%s--\r\n", boundary)
}

func formatAddresses(list []emailAddress) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		name := ""
		if a.Name != nil {
			name = *a.Name
		}
		parts = append(parts, (&mail.Address{Name: name, Address: a.Email}).String())
	}
	return strings.Join(parts, ", ")
}

func angleList(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "<" + strings.Trim(id, "<>") + ">"
	}
	return strings.Join(out, " ")
}

func newMessageID(from []emailAddress) string {
	domain := "czlmail.gateway"
	if len(from) > 0 {
		if _, d, ok := strings.Cut(from[0].Email, "@"); ok && d != "" {
			domain = d
		}
	}
	return randomHex(16) + "@" + domain
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
