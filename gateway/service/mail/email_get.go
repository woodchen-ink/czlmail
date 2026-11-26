package mail

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

var emailProperties = []string{"id", "blobId", "threadId", "mailboxIds", "keywords", "size", "receivedAt",
	"messageId", "inReplyTo", "references", "sender", "from", "to", "cc", "bcc", "replyTo", "subject",
	"sentAt", "hasAttachment", "preview", "headers", "bodyStructure", "bodyValues", "textBody", "htmlBody",
	"attachments"}

var emailDefaults = []string{"id", "blobId", "threadId", "mailboxIds", "keywords", "size", "receivedAt",
	"messageId", "inReplyTo", "references", "sender", "from", "to", "cc", "bcc", "replyTo", "subject",
	"sentAt", "hasAttachment", "preview", "bodyValues", "textBody", "htmlBody", "attachments"}

// emailGetArgs are Email/get's arguments (RFC 8621 §4.2).
type emailGetArgs struct {
	getArgs
	BodyProperties      *[]string `json:"bodyProperties"`
	FetchTextBodyValues bool      `json:"fetchTextBodyValues"`
	FetchHTMLBodyValues bool      `json:"fetchHTMLBodyValues"`
	FetchAllBodyValues  bool      `json:"fetchAllBodyValues"`
	MaxBodyValueBytes   int       `json:"maxBodyValueBytes"`
}

// propSet is a validated properties list: plain names and header:* forms.
type propSet struct {
	names   map[string]bool
	headers []string // header:* properties as requested
	parsed  map[string]headerProp
}

func (p *propSet) has(name string) bool { return p.names[name] }

func parseProps(req *[]string, known, defaults []string, always string) (*propSet, error) {
	list := defaults
	if req != nil {
		list = *req
	}
	ps := &propSet{names: map[string]bool{}, parsed: map[string]headerProp{}}
	for _, p := range list {
		if strings.HasPrefix(p, "header:") {
			hp, ok := parseHeaderProp(p)
			if !ok {
				return nil, jmapserver.InvalidArguments(fmt.Sprintf("invalid header property %q", p))
			}
			if _, dup := ps.parsed[p]; !dup {
				ps.headers = append(ps.headers, p)
				ps.parsed[p] = hp
			}
			continue
		}
		if !slices.Contains(known, p) {
			return nil, jmapserver.InvalidArguments(fmt.Sprintf("unknown property %q", p))
		}
		ps.names[p] = true
	}
	if always != "" {
		ps.names[always] = true
	}
	return ps, nil
}

// getter builds Email objects for one Email/get.
type getter struct {
	s      *Service
	a      *account
	props  *propSet
	bprops *propSet
	args   *emailGetArgs
}

func (g *getter) needsBody() bool {
	p := g.props
	return p.has("bodyStructure") || p.has("textBody") || p.has("htmlBody") || p.has("attachments") || p.has("bodyValues")
}

func (g *getter) needsHeader() bool { return g.props.has("headers") || len(g.props.headers) > 0 }

func (s *Service) emailGet(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args emailGetArgs
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if err := checkIDs(args.IDs); err != nil {
		return nil, err
	}
	if args.MaxBodyValueBytes < 0 {
		return nil, jmapserver.InvalidArguments("maxBodyValueBytes must not be negative")
	}
	props, err := parseProps(args.Properties, emailProperties, emailDefaults, "id")
	if err != nil {
		return nil, err
	}
	bprops, err := parseProps(args.BodyProperties, bodyPartProperties, bodyPartDefaults, "")
	if err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeEmail)
	var ids []string
	if args.IDs == nil {
		// "All" is bounded by maxObjectsInGet: the newest emails.
		if err := a.idx.DB().Table("emails").Where("orphaned_from = ''").Order("received_at DESC").
			Limit(jmapserver.MaxObjectsInGet).Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
	} else {
		for _, id := range dedupe(*args.IDs) {
			real, _ := resolveID(call, id)
			ids = append(ids, real)
		}
	}
	msgs, err := loadMessages(a.idx, ids)
	if err != nil {
		return nil, err
	}
	g := &getter{s: s, a: a, props: props, bprops: bprops, args: &args}
	upstreamNeeded := g.needsHeader() || props.has("bodyValues") && (args.FetchAllBodyValues || args.FetchHTMLBodyValues || args.FetchTextBodyValues) ||
		g.needsBody() && (bprops.has("headers") || len(bprops.headers) > 0)
	if upstreamNeeded && len(msgs) > 0 {
		if err := a.online(); err != nil {
			return nil, err
		}
	}

	objs := make([]map[string]any, len(ids))
	errs := make([]error, len(ids))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, id := range ids {
		m := msgs[id]
		if m == nil {
			continue
		}
		if !upstreamNeeded && !(g.needsBody() && m.row.BodyStructure == "") {
			objs[i], errs[i] = g.build(ctx, m)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			objs[i], errs[i] = g.build(ctx, m)
		}()
	}
	wg.Wait()

	list := []map[string]any{}
	notFound := []string{}
	requested := ids
	if args.IDs != nil {
		requested = dedupe(*args.IDs)
	}
	for i := range ids {
		if errs[i] != nil {
			if messageGone(errs[i]) {
				notFound = append(notFound, requested[i])
				continue
			}
			if me, ok := unavailable(errs[i]); ok {
				return nil, me
			}
			return nil, errs[i]
		}
		if objs[i] == nil {
			notFound = append(notFound, requested[i])
			continue
		}
		list = append(list, objs[i])
	}
	return map[string]any{"accountId": a.id, "state": state, "list": list, "notFound": notFound}, nil
}

// build makes one Email object.
func (g *getter) build(ctx context.Context, m *message) (map[string]any, error) {
	r := &m.row
	o := map[string]any{"id": r.ID}
	p := g.props
	put := func(name string, v func() any) {
		if p.has(name) {
			o[name] = v()
		}
	}
	put("blobId", func() any { return blobIDMessage(r.ID) })
	put("threadId", func() any { return r.ThreadID })
	put("mailboxIds", func() any {
		mb := map[string]bool{}
		for _, id := range m.mailboxes {
			mb[id] = true
		}
		return mb
	})
	put("keywords", func() any { return keywordsOf(r.Keywords) })
	put("size", func() any { return r.Size })
	put("receivedAt", func() any { return utcDate(r.ReceivedAt) })
	put("messageId", func() any {
		if r.MessageID == "" {
			return nil
		}
		return []string{r.MessageID}
	})
	put("inReplyTo", func() any { return stringsOrNull(r.InReplyTo) })
	put("references", func() any { return stringsOrNull(r.References) })
	put("sender", func() any { return addressesOrNull(r.Sender) })
	put("from", func() any { return addressesOrNull(r.From) })
	put("to", func() any { return addressesOrNull(r.To) })
	put("cc", func() any { return addressesOrNull(r.Cc) })
	put("bcc", func() any { return addressesOrNull(r.Bcc) })
	put("replyTo", func() any { return addressesOrNull(r.ReplyTo) })
	put("subject", func() any { return r.Subject })
	put("sentAt", func() any {
		if r.SentAt == nil {
			return nil
		}
		return r.SentAt.Format(time.RFC3339)
	})
	put("hasAttachment", func() any { return r.HasAttachment })
	put("preview", func() any { return r.Preview })

	if g.needsHeader() {
		head, err := g.s.readSection(ctx, g.a, m, "HEADER", 0)
		if err != nil {
			return nil, err
		}
		fields := headerFields(head)
		if p.has("headers") {
			o["headers"] = headersJSON(fields)
		}
		for _, name := range p.headers {
			o[name] = p.parsed[name].value(fields)
		}
	}
	if !g.needsBody() {
		return o, nil
	}
	bl, err := g.s.structure(ctx, g.a, m)
	if err != nil {
		return nil, err
	}
	partObj := func(pt *part) (map[string]any, error) { return g.partObject(ctx, m, pt) }
	list := func(parts []*part) (any, error) {
		out := make([]map[string]any, 0, len(parts))
		for _, pt := range parts {
			po, err := partObj(pt)
			if err != nil {
				return nil, err
			}
			out = append(out, po)
		}
		return out, nil
	}
	if p.has("bodyStructure") {
		v, err := partObj(bl.tree)
		if err != nil {
			return nil, err
		}
		o["bodyStructure"] = v
	}
	for name, parts := range map[string][]*part{"textBody": bl.textBody, "htmlBody": bl.htmlBody, "attachments": bl.attachments} {
		if p.has(name) {
			v, err := list(parts)
			if err != nil {
				return nil, err
			}
			o[name] = v
		}
	}
	if p.has("bodyValues") {
		values, err := g.bodyValues(ctx, m, bl)
		if err != nil {
			return nil, err
		}
		o["bodyValues"] = values
	}
	return o, nil
}

type bodyValue struct {
	Value             string `json:"value"`
	IsEncodingProblem bool   `json:"isEncodingProblem"`
	IsTruncated       bool   `json:"isTruncated"`
}

func (g *getter) bodyValues(ctx context.Context, m *message, bl *bodyLists) (map[string]bodyValue, error) {
	out := map[string]bodyValue{}
	var want []*part
	switch {
	case g.args.FetchAllBodyValues:
		for _, pt := range bl.byID {
			want = append(want, pt)
		}
	default:
		if g.args.FetchTextBodyValues {
			want = append(want, bl.textBody...)
		}
		if g.args.FetchHTMLBodyValues {
			want = append(want, bl.htmlBody...)
		}
	}
	for _, pt := range want {
		if !pt.isText() {
			continue
		}
		if _, done := out[pt.partID]; done {
			continue
		}
		data, err := g.s.readSection(ctx, g.a, m, pt.partID, 0)
		if err != nil {
			return nil, err
		}
		dec, p1 := decodeTransfer(data, pt.node.Encoding)
		cs, _ := pt.charset().(string)
		text, p2 := decodeCharset(dec, cs)
		v := bodyValue{Value: text, IsEncodingProblem: p1 || p2}
		if max := g.args.MaxBodyValueBytes; max > 0 && len(text) > max {
			v.Value, v.IsTruncated = truncateUTF8(text, max), true
		}
		out[pt.partID] = v
	}
	return out, nil
}

// partObject is one EmailBodyPart with the requested bodyProperties. A
// multipart node always carries subParts, so clients can walk the tree.
func (g *getter) partObject(ctx context.Context, m *message, pt *part) (map[string]any, error) {
	bp := g.bprops
	o := map[string]any{}
	put := func(name string, v func() any) {
		if bp.has(name) {
			o[name] = v()
		}
	}
	leaf := !pt.multipart()
	put("partId", func() any { return nullable(pt.partID) })
	put("blobId", func() any {
		if !leaf {
			return nil
		}
		return blobIDPart(m.row.ID, pt.partID)
	})
	put("size", func() any {
		if !leaf {
			return 0
		}
		return pt.decodedSize()
	})
	put("name", func() any { return nullable(pt.name()) })
	put("type", func() any { return pt.mediaType() })
	put("charset", func() any { return pt.charset() })
	put("disposition", func() any { return nullable(pt.node.Disposition) })
	put("cid", func() any { return nullable(strings.Trim(strings.TrimSpace(pt.node.ID), "<>")) })
	put("language", func() any {
		if len(pt.node.Language) == 0 {
			return nil
		}
		return pt.node.Language
	})
	put("location", func() any { return nullable(pt.node.Location) })
	if bp.has("headers") || len(bp.headers) > 0 {
		head, err := g.s.readSection(ctx, g.a, m, pt.headerSection(), 0)
		if err != nil {
			return nil, err
		}
		fields := headerFields(head) // the root part's header is the message's
		if bp.has("headers") {
			o["headers"] = headersJSON(fields)
		}
		for _, name := range bp.headers {
			o[name] = bp.parsed[name].value(fields)
		}
	}
	if !leaf {
		subs := []map[string]any{}
		for _, c := range g.s.childrenOf(m, pt) {
			co, err := g.partObject(ctx, m, c)
			if err != nil {
				return nil, err
			}
			subs = append(subs, co)
		}
		o["subParts"] = subs
	}
	return o, nil
}

func (s *Service) childrenOf(m *message, pt *part) []*part { return m.body.children[pt] }

func keywordsOf(js string) map[string]bool {
	k := mailindex.ParseKeywords(js)
	out := make(map[string]bool, len(k))
	for kw, on := range k {
		if on {
			out[kw] = true
		}
	}
	return out
}

func utcDate(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func stringsOrNull(js string) any {
	l := mailindex.DecodeStrings(js)
	if len(l) == 0 {
		return nil
	}
	return l
}

func addressesOrNull(js string) any {
	l := mailindex.DecodeAddresses(js)
	if len(l) == 0 {
		return nil
	}
	out := make([]emailAddress, 0, len(l))
	for _, a := range l {
		var name *string
		if a.Name != "" {
			n := a.Name
			name = &n
		}
		out = append(out, emailAddress{Name: name, Email: a.Email})
	}
	return out
}
