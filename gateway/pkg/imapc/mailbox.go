package imapc

import (
	"context"
	"strings"
)

// Mailbox is the selected mailbox as SELECT reported it, kept current by the
// responses of later commands.
type Mailbox struct {
	Name           string
	ReadOnly       bool
	Exists         uint32
	UIDValidity    uint32
	UIDNext        uint32
	HighestModSeq  uint64
	NoModSeq       bool
	PermanentFlags []string
	// MailboxID is the OBJECTID MAILBOXID, when the server has OBJECTID.
	MailboxID string
	// Vanished collects UIDs from VANISHED (EARLIER) of a QRESYNC SELECT, and
	// VANISHED or EXPUNGE-derived removals seen later are not tracked here.
	Vanished UIDSet
	// Changed are the FETCH responses SELECT (QRESYNC) returned.
	Changed []*Message
}

func (m *Mailbox) track(r *Response) {
	switch r.Type {
	case "EXISTS":
		m.Exists = r.Num
	case "EXPUNGE":
		if m.Exists > 0 {
			m.Exists--
		}
	case "OK":
		switch r.Code {
		case "UIDNEXT":
			m.UIDNext = uint32(numArg(r.CodeArgs))
		case "HIGHESTMODSEQ":
			m.HighestModSeq = numArg(r.CodeArgs)
		}
	}
}

// QResync are the parameters of SELECT (QRESYNC (uidvalidity modseq)).
type QResync struct {
	UIDValidity uint32
	ModSeq      uint64
}

// SelectOptions configure Select.
type SelectOptions struct {
	ReadOnly bool // EXAMINE
	CondStore bool
	// QResync requires ENABLE QRESYNC first.
	QResync *QResync
}

// Select selects a mailbox. With QResync the server reports expunged UIDs in
// Mailbox.Vanished and changed messages in Mailbox.Changed.
func (c *Client) Select(ctx context.Context, name string, opts SelectOptions) (*Mailbox, error) {
	cmd := "SELECT"
	if opts.ReadOnly {
		cmd = "EXAMINE"
	}
	args := []any{MailboxName(name)}
	switch {
	case opts.QResync != nil:
		args = append(args, []any{Atom("QRESYNC"), []any{opts.QResync.UIDValidity, opts.QResync.ModSeq}})
	case opts.CondStore:
		args = append(args, []any{Atom("CONDSTORE")})
	}
	mb := &Mailbox{Name: name, ReadOnly: opts.ReadOnly}
	c.selected = nil
	resp, err := c.Execute(ctx, cmd, func(r *Response) {
		switch r.Type {
		case "EXISTS":
			mb.Exists = r.Num
		case "VANISHED":
			if set, ok := parseVanished(r); ok {
				mb.Vanished = append(mb.Vanished, set...)
			}
		case "FETCH":
			if m, err := parseFetch(r); err == nil {
				mb.Changed = append(mb.Changed, m)
			}
		case "OK":
			switch r.Code {
			case "UIDVALIDITY":
				mb.UIDValidity = uint32(numArg(r.CodeArgs))
			case "UIDNEXT":
				mb.UIDNext = uint32(numArg(r.CodeArgs))
			case "HIGHESTMODSEQ":
				mb.HighestModSeq = numArg(r.CodeArgs)
			case "NOMODSEQ":
				mb.NoModSeq = true
			case "MAILBOXID":
				if len(r.CodeArgs) > 0 && r.CodeArgs[0].Kind == KindList && len(r.CodeArgs[0].List) > 0 {
					mb.MailboxID = r.CodeArgs[0].List[0].String()
				}
			case "PERMANENTFLAGS":
				if len(r.CodeArgs) > 0 {
					mb.PermanentFlags = flagList(r.CodeArgs[0])
				}
			}
		}
	}, args...)
	if err != nil {
		return nil, err
	}
	if resp.Code == "READ-ONLY" {
		mb.ReadOnly = true
	}
	c.selected = mb
	return mb, nil
}

// Selected returns the selected mailbox, or nil.
func (c *Client) Selected() *Mailbox { return c.selected }

// Unselect leaves the selected mailbox without expunging (UNSELECT, else CLOSE
// on a read-only EXAMINE of the same mailbox is not needed: callers re-SELECT).
func (c *Client) Unselect(ctx context.Context) error {
	if c.selected == nil {
		return nil
	}
	var err error
	if c.caps["UNSELECT"] {
		_, err = c.Execute(ctx, "UNSELECT", nil)
	} else {
		_, err = c.Execute(ctx, "CLOSE", nil)
	}
	c.selected = nil
	return err
}

func parseVanished(r *Response) (UIDSet, bool) {
	f := r.Fields
	if len(f) > 0 && f[0].Kind == KindList { // (EARLIER)
		f = f[1:]
	}
	if len(f) == 0 {
		return nil, false
	}
	set, err := ParseUIDSet(f[0].String())
	return set, err == nil
}

func numArg(args []Value) uint64 {
	if len(args) == 0 {
		return 0
	}
	return args[0].Num
}

func flagList(v Value) []string {
	out := make([]string, 0, len(v.List))
	for _, f := range v.List {
		out = append(out, f.String())
	}
	return out
}

// ListEntry is one LIST response, with STATUS when LIST-STATUS was used.
type ListEntry struct {
	Name      string // decoded from modified UTF-7
	Delimiter string
	Attrs     []string // as sent, e.g. \HasNoChildren, \Sent, \Noselect
	Status    *Status
}

// HasAttr reports an attribute case-insensitively.
func (e *ListEntry) HasAttr(a string) bool {
	for _, x := range e.Attrs {
		if strings.EqualFold(x, a) {
			return true
		}
	}
	return false
}

// Status is a STATUS response.
type Status struct {
	Messages      uint32
	UIDNext       uint32
	UIDValidity   uint32
	Unseen        uint32
	HighestModSeq uint64
	MailboxID     string
	HasModSeq     bool
}

// StatusItems the gateway asks for; HIGHESTMODSEQ only with CONDSTORE.
func (c *Client) StatusItems() []any {
	items := []any{Atom("MESSAGES"), Atom("UIDNEXT"), Atom("UIDVALIDITY"), Atom("UNSEEN")}
	if c.caps["CONDSTORE"] {
		items = append(items, Atom("HIGHESTMODSEQ"))
	}
	if c.caps["OBJECTID"] {
		items = append(items, Atom("MAILBOXID"))
	}
	return items
}

// List lists every mailbox. With LIST-STATUS it also returns each mailbox's status
// in one round trip; SPECIAL-USE attributes are requested when supported.
func (c *Client) List(ctx context.Context, withStatus bool) ([]*ListEntry, error) {
	var ret []any
	if c.caps["SPECIAL-USE"] {
		ret = append(ret, Atom("SPECIAL-USE"))
	}
	if withStatus && c.caps["LIST-STATUS"] {
		ret = append(ret, Atom("STATUS"), c.StatusItems())
	}
	args := []any{"", "*"}
	if len(ret) > 0 {
		args = append(args, Atom("RETURN"), ret)
	}
	var out []*ListEntry
	byName := map[string]*ListEntry{}
	_, err := c.Execute(ctx, "LIST", func(r *Response) {
		switch r.Type {
		case "LIST":
			if e := parseList(r); e != nil {
				out = append(out, e)
				byName[e.Name] = e
			}
		case "STATUS":
			if name, st := parseStatus(r); st != nil {
				if e := byName[name]; e != nil {
					e.Status = st
				}
			}
		}
	}, args...)
	return out, err
}

func parseList(r *Response) *ListEntry {
	f := r.Fields
	if len(f) < 3 || f[0].Kind != KindList {
		return nil
	}
	return &ListEntry{Name: DecodeMailbox(f[2].String()), Delimiter: f[1].String(), Attrs: flagList(f[0])}
}

// ParseStatus reads a STATUS response (also delivered by NOTIFY).
func ParseStatus(r *Response) (string, *Status) { return parseStatus(r) }

func parseStatus(r *Response) (string, *Status) {
	f := r.Fields
	if len(f) < 2 || f[1].Kind != KindList {
		return "", nil
	}
	st := &Status{}
	items := f[1].List
	for i := 0; i+1 < len(items); i += 2 {
		v := items[i+1]
		switch strings.ToUpper(items[i].String()) {
		case "MESSAGES":
			st.Messages = uint32(v.Num)
		case "UIDNEXT":
			st.UIDNext = uint32(v.Num)
		case "UIDVALIDITY":
			st.UIDValidity = uint32(v.Num)
		case "UNSEEN":
			st.Unseen = uint32(v.Num)
		case "HIGHESTMODSEQ":
			st.HighestModSeq, st.HasModSeq = v.Num, true
		case "MAILBOXID":
			if v.Kind == KindList && len(v.List) > 0 {
				st.MailboxID = v.List[0].String()
			}
		}
	}
	return DecodeMailbox(f[0].String()), st
}

// Status asks for one mailbox's status.
func (c *Client) Status(ctx context.Context, name string) (*Status, error) {
	var st *Status
	_, err := c.Execute(ctx, "STATUS", func(r *Response) {
		if r.Type == "STATUS" {
			if _, s := parseStatus(r); s != nil {
				st = s
			}
		}
	}, MailboxName(name), c.StatusItems())
	if err == nil && st == nil {
		st = &Status{}
	}
	return st, err
}

// Create creates a mailbox.
func (c *Client) Create(ctx context.Context, name string) error {
	_, err := c.Execute(ctx, "CREATE", nil, MailboxName(name))
	return err
}

// Rename renames a mailbox.
func (c *Client) Rename(ctx context.Context, from, to string) error {
	_, err := c.Execute(ctx, "RENAME", nil, MailboxName(from), MailboxName(to))
	return err
}

// Delete deletes a mailbox.
func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.Execute(ctx, "DELETE", nil, MailboxName(name))
	return err
}

// Subscribe subscribes to a mailbox; errors are ignored by most callers.
func (c *Client) Subscribe(ctx context.Context, name string) error {
	_, err := c.Execute(ctx, "SUBSCRIBE", nil, MailboxName(name))
	return err
}
