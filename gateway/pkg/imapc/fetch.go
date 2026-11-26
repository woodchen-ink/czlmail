package imapc

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"
)

// Message is one FETCH response.
type Message struct {
	SeqNum       uint32
	UID          uint32
	Flags        []string
	HasFlags     bool
	InternalDate time.Time
	Size         int64
	ModSeq       uint64
	Envelope     *Envelope
	Body         *BodyPart
	// Sections are BODY[...] values keyed by the section as the server named it,
	// upper-cased, without "BODY" and the origin: "HEADER.FIELDS (REFERENCES)", "1.2", "".
	Sections map[string]string
	// OBJECTID (RFC 8474) and Dovecot X-GUID.
	EmailID  string
	ThreadID string
	XGUID    string
	// Extra holds items this package does not interpret (X-GM-MSGID ...), by upper-case name.
	Extra map[string]Value
}

// Envelope is the ENVELOPE of a message. Strings are as sent (RFC 2047 encoded words
// are not decoded here).
type Envelope struct {
	Date      string
	Subject   string
	From      []Address
	Sender    []Address
	ReplyTo   []Address
	To        []Address
	Cc        []Address
	Bcc       []Address
	InReplyTo string
	MessageID string
}

// Address is one envelope address.
type Address struct {
	Name    string
	Mailbox string
	Host    string
}

// Email is mailbox@host, or "" for group syntax markers.
func (a Address) Email() string {
	if a.Host == "" {
		return a.Mailbox
	}
	return a.Mailbox + "@" + a.Host
}

// BodyPart is one node of a BODYSTRUCTURE. Path is the IMAP section number
// ("1", "2.1"); the root of a multipart message has Path "".
type BodyPart struct {
	Path        string            `json:"path"`
	Type        string            `json:"type"`    // lower-case, e.g. "text"
	Subtype     string            `json:"subtype"` // lower-case, e.g. "plain"
	Params      map[string]string `json:"params,omitempty"`
	ID          string            `json:"id,omitempty"`
	Description string            `json:"description,omitempty"`
	Encoding    string            `json:"encoding,omitempty"` // lower-case
	Size        int64             `json:"size"`
	Lines       int64             `json:"lines,omitempty"`
	Disposition string            `json:"disposition,omitempty"` // lower-case
	DispParams  map[string]string `json:"dispParams,omitempty"`
	Language    []string          `json:"language,omitempty"`
	Location    string            `json:"location,omitempty"`
	Parts       []*BodyPart       `json:"parts,omitempty"`
}

// MediaType is "type/subtype".
func (p *BodyPart) MediaType() string { return p.Type + "/" + p.Subtype }

// Filename is the disposition filename or the content-type name parameter.
func (p *BodyPart) Filename() string {
	if f := p.DispParams["filename"]; f != "" {
		return f
	}
	return p.Params["name"]
}

// Walk visits the part and its descendants depth first.
func (p *BodyPart) Walk(fn func(*BodyPart)) {
	fn(p)
	for _, c := range p.Parts {
		c.Walk(fn)
	}
}

// FetchOptions modify a FETCH.
type FetchOptions struct {
	UID          bool   // UID FETCH
	ChangedSince uint64 // CONDSTORE (CHANGEDSINCE n)
	Vanished     bool   // QRESYNC (VANISHED); needs UID and ChangedSince
}

// Fetch runs [UID] FETCH set items and calls fn for every message. items are raw
// item names such as "UID", "FLAGS", "BODY.PEEK[HEADER.FIELDS (REFERENCES)]".
// VANISHED (EARLIER) responses go to vanished, which may be nil.
func (c *Client) Fetch(ctx context.Context, set string, items []string, opts FetchOptions, fn func(*Message), vanished func(UIDSet)) error {
	return c.fetch(ctx, set, items, opts, fn, vanished, nil)
}

// FetchSink is Fetch with literals streamed to sink (for message bodies).
func (c *Client) FetchSink(ctx context.Context, set string, items []string, opts FetchOptions, fn func(*Message), sink LiteralSink) error {
	return c.fetch(ctx, set, items, opts, fn, nil, sink)
}

func (c *Client) fetch(ctx context.Context, set string, items []string, opts FetchOptions, fn func(*Message), vanished func(UIDSet), sink LiteralSink) error {
	name := "FETCH"
	if opts.UID {
		name = "UID FETCH"
	}
	list := make([]any, len(items))
	for i, it := range items {
		list[i] = Atom(it)
	}
	args := []any{Atom(set), list}
	if opts.ChangedSince > 0 {
		mod := []any{Atom("CHANGEDSINCE"), opts.ChangedSince}
		if opts.Vanished {
			mod = append(mod, Atom("VANISHED"))
		}
		args = append(args, mod)
	}
	var perr error
	_, err := c.execute(ctx, name, func(r *Response) {
		switch r.Type {
		case "FETCH":
			m, err := parseFetch(r)
			if err != nil {
				perr = err
				return
			}
			if fn != nil {
				fn(m)
			}
		case "VANISHED":
			if set, ok := parseVanished(r); ok && vanished != nil {
				vanished(set)
			}
		}
	}, sink, args...)
	if err != nil {
		return err
	}
	return perr
}

// ParseFetch interprets a FETCH response (also delivered unsolicited).
func ParseFetch(r *Response) (*Message, error) { return parseFetch(r) }

func parseFetch(r *Response) (*Message, error) {
	if len(r.Fields) == 0 || r.Fields[0].Kind != KindList {
		return nil, protoErr("FETCH without item list")
	}
	m := &Message{SeqNum: r.Num}
	items := r.Fields[0].List
	for i := 0; i+1 < len(items); i += 2 {
		name := strings.ToUpper(items[i].Str)
		v := items[i+1]
		switch {
		case name == "UID":
			m.UID = uint32(v.Num)
		case name == "FLAGS":
			m.Flags, m.HasFlags = flagList(v), true
		case name == "INTERNALDATE":
			m.InternalDate = parseInternalDate(v.String())
		case name == "RFC822.SIZE":
			m.Size = int64(v.Num)
		case name == "MODSEQ":
			if v.Kind == KindList && len(v.List) > 0 {
				m.ModSeq = v.List[0].Num
			}
		case name == "ENVELOPE":
			m.Envelope = parseEnvelope(v)
		case name == "BODYSTRUCTURE" || name == "BODY":
			m.Body = parseBody(v, "")
			if m.Body != nil && len(m.Body.Parts) == 0 {
				m.Body.Path = "1"
			}
		case strings.HasPrefix(name, "BODY[") || strings.HasPrefix(name, "BINARY["):
			if m.Sections == nil {
				m.Sections = map[string]string{}
			}
			m.Sections[sectionKey(name)] = v.Str
		case name == "EMAILID":
			m.EmailID = firstOfList(v)
		case name == "THREADID":
			m.ThreadID = firstOfList(v)
		case name == "X-GUID":
			m.XGUID = v.String()
		default:
			if m.Extra == nil {
				m.Extra = map[string]Value{}
			}
			m.Extra[name] = v
		}
	}
	return m, nil
}

// sectionKey turns BODY[HEADER.FIELDS (REFERENCES)]<0> into HEADER.FIELDS (REFERENCES).
func sectionKey(name string) string {
	open := strings.IndexByte(name, '[')
	close := strings.LastIndexByte(name, ']')
	if open < 0 || close < open {
		return name
	}
	return name[open+1 : close]
}

func firstOfList(v Value) string {
	if v.Kind == KindList {
		if len(v.List) == 0 {
			return ""
		}
		return v.List[0].String()
	}
	return v.String()
}

func parseInternalDate(s string) time.Time {
	t, err := time.Parse("_2-Jan-2006 15:04:05 -0700", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseEnvelope(v Value) *Envelope {
	if v.Kind != KindList || len(v.List) < 10 {
		return nil
	}
	l := v.List
	return &Envelope{
		Date: l[0].String(), Subject: l[1].String(),
		From: parseAddrs(l[2]), Sender: parseAddrs(l[3]), ReplyTo: parseAddrs(l[4]),
		To: parseAddrs(l[5]), Cc: parseAddrs(l[6]), Bcc: parseAddrs(l[7]),
		InReplyTo: l[8].String(), MessageID: l[9].String(),
	}
}

func parseAddrs(v Value) []Address {
	if v.Kind != KindList {
		return nil
	}
	var out []Address
	for _, a := range v.List {
		if a.Kind != KindList || len(a.List) < 4 {
			continue
		}
		addr := Address{Name: a.List[0].String(), Mailbox: a.List[2].String(), Host: a.List[3].String()}
		if a.List[3].Kind == KindNil {
			continue // group start / end marker
		}
		out = append(out, addr)
	}
	return out
}

// parseBody reads BODY / BODYSTRUCTURE. path is the section number of this node.
func parseBody(v Value, path string) *BodyPart {
	if v.Kind != KindList || len(v.List) == 0 {
		return nil
	}
	l := v.List
	if l[0].Kind == KindList { // multipart
		p := &BodyPart{Path: path, Type: "multipart"}
		i := 0
		for ; i < len(l) && l[i].Kind == KindList; i++ {
			p.Parts = append(p.Parts, parseBody(l[i], childPath(path, i+1)))
		}
		if i < len(l) {
			p.Subtype = strings.ToLower(l[i].String())
			i++
		}
		if i < len(l) {
			p.Params = parseParams(l[i])
			i++
		}
		if i < len(l) {
			p.Disposition, p.DispParams = parseDisposition(l[i])
			i++
		}
		parseLangLoc(p, l, i)
		return p
	}
	if len(l) < 7 {
		return nil
	}
	p := &BodyPart{
		Path:        path,
		Type:        strings.ToLower(l[0].String()),
		Subtype:     strings.ToLower(l[1].String()),
		Params:      parseParams(l[2]),
		ID:          l[3].String(),
		Description: l[4].String(),
		Encoding:    strings.ToLower(l[5].String()),
		Size:        int64(l[6].Num),
	}
	i := 7
	switch {
	case p.Type == "text":
		if i < len(l) {
			p.Lines = int64(l[i].Num)
			i++
		}
	case p.Type == "message" && (p.Subtype == "rfc822" || p.Subtype == "global") && len(l) >= 10:
		inner := parseBody(l[8], path)
		if inner != nil {
			if len(inner.Parts) == 0 {
				inner.Path = childPath(path, 1)
				p.Parts = []*BodyPart{inner}
			} else {
				for j, c := range inner.Parts {
					reparent(c, childPath(path, j+1))
				}
				inner.Path = path
				p.Parts = []*BodyPart{inner}
			}
		}
		p.Lines = int64(l[9].Num)
		i = 10
	}
	if i < len(l) {
		i++ // md5
	}
	if i < len(l) {
		p.Disposition, p.DispParams = parseDisposition(l[i])
		i++
	}
	parseLangLoc(p, l, i)
	return p
}

func reparent(p *BodyPart, path string) {
	p.Path = path
	for j, c := range p.Parts {
		reparent(c, childPath(path, j+1))
	}
}

func childPath(parent string, n int) string {
	if parent == "" {
		return strconv.Itoa(n)
	}
	return parent + "." + strconv.Itoa(n)
}

func parseLangLoc(p *BodyPart, l []Value, i int) {
	if i < len(l) {
		if l[i].Kind == KindList {
			for _, x := range l[i].List {
				p.Language = append(p.Language, x.String())
			}
		} else if s := l[i].String(); s != "" {
			p.Language = []string{s}
		}
		i++
	}
	if i < len(l) {
		p.Location = l[i].String()
	}
}

func parseParams(v Value) map[string]string {
	if v.Kind != KindList || len(v.List) < 2 {
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(v.List); i += 2 {
		out[strings.ToLower(v.List[i].String())] = v.List[i+1].String()
	}
	return out
}

func parseDisposition(v Value) (string, map[string]string) {
	if v.Kind != KindList || len(v.List) == 0 {
		return "", nil
	}
	d := strings.ToLower(v.List[0].String())
	var params map[string]string
	if len(v.List) > 1 {
		params = parseParams(v.List[1])
	}
	return d, params
}

// FetchBody streams BODY.PEEK[section] of one UID into w; section "" is the whole
// message. It returns false when the server sent no such message.
func (c *Client) FetchBody(ctx context.Context, uid uint32, section string, w io.Writer) (bool, error) {
	found := false
	err := c.FetchSink(ctx, strconv.FormatUint(uint64(uid), 10), []string{"BODY.PEEK[" + section + "]"}, FetchOptions{UID: true},
		func(m *Message) {
			if m.UID == uid || m.UID == 0 {
				found = true
			}
		},
		func(int64) io.Writer { return w })
	return found, err
}
