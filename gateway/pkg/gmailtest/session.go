package gmailtest

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

const (
	capsPreAuth = "IMAP4rev1 UNSELECT IDLE NAMESPACE ID XLIST CHILDREN X-GM-EXT-1 SASL-IR AUTH=XOAUTH2 AUTH=PLAIN"
	capsAuth    = "IMAP4rev1 UNSELECT IDLE NAMESPACE ID XLIST CHILDREN X-GM-EXT-1 UIDPLUS ENABLE MOVE CONDSTORE LIST-EXTENDED LIST-STATUS LITERAL- SPECIAL-USE"
)

// errBye ends the session.
var errBye = errors.New("bye")

type session struct {
	s     *Server
	tag   string // tag of the command being run
	conn  net.Conn
	br    *bufio.Reader
	bw    *bufio.Writer
	authd bool

	sel       *folder // selected folder
	readOnly  bool
	view      []uint32 // UIDs the client knows, in sequence-number order
	seenMod   uint64   // modseq up to which the client has been told about flag changes
	condstore bool
}

func newSession(s *Server, c net.Conn) *session {
	return &session{s: s, conn: c, br: bufio.NewReader(c), bw: bufio.NewWriter(c)}
}

func (ss *session) out(format string, args ...any) {
	fmt.Fprintf(ss.bw, format, args...)
	ss.bw.WriteString("\r\n")
}

func (ss *session) flush() { _ = ss.bw.Flush() }

func (ss *session) serve() {
	ss.out("* OK Gimap ready for requests from gmailtest")
	ss.flush()
	for {
		line, err := readCommand(ss.br, func() { ss.out("+ go ahead"); ss.flush() })
		if err != nil {
			return
		}
		tag, rest, _ := strings.Cut(line, " ")
		if tag == "" {
			continue
		}
		name, args, _ := strings.Cut(rest, " ")
		err = ss.dispatch(tag, strings.ToUpper(name), args, rest)
		ss.flush()
		if errors.Is(err, errBye) {
			return
		}
	}
}

func (ss *session) dispatch(tag, name, args, full string) error {
	ss.tag = tag
	ss.s.mu.Lock()
	intercept := ss.s.intercept
	ss.s.mu.Unlock()
	if ss.authd && intercept != nil {
		switch st := intercept(full); st {
		case "":
		case "BYE":
			return errBye
		default:
			ss.out("%s %s", tag, st)
			return nil
		}
	}
	p := &parser{s: args}
	var err error
	switch name {
	case "CAPABILITY":
		ss.out("* CAPABILITY %s", ss.caps())
	case "NOOP":
	case "LOGOUT":
		ss.out("* BYE LOGOUT Requested")
		ss.out("%s OK 73 good day (Success)", tag)
		return errBye
	case "ID":
		ss.out(`* ID ("name" "gmailtest" "vendor" "czlmail")`)
	case "LOGIN":
		return ss.login(tag, p)
	case "AUTHENTICATE":
		return ss.authenticate(tag, p)
	default:
		if !ss.authd {
			ss.out("%s BAD command not valid before authentication", tag)
			return nil
		}
		err = ss.authCommand(tag, name, p)
	}
	if errors.Is(err, errBye) {
		return err
	}
	if errors.Is(err, errAnswered) {
		return nil
	}
	if err != nil {
		var no noErr
		if errors.As(err, &no) {
			ss.out("%s NO %s", tag, no.msg)
		} else {
			ss.out("%s BAD %v", tag, err)
		}
		return nil
	}
	if name != "FETCH" && name != "STORE" && name != "SEARCH" && name != "LOGOUT" {
		ss.pushUpdates()
	}
	if ss.authd || name == "CAPABILITY" || name == "NOOP" || name == "ID" {
		ss.out("%s OK %s completed (Success)", tag, name)
	}
	return nil
}

// noErr is a NO response.
type noErr struct{ msg string }

func (e noErr) Error() string { return e.msg }

func no(format string, args ...any) error { return noErr{fmt.Sprintf(format, args...)} }

func (ss *session) caps() string {
	if !ss.authd {
		return capsPreAuth
	}
	if ss.s.opts.ObjectID {
		return capsAuth + " OBJECTID"
	}
	return capsAuth
}

func (ss *session) authCommand(tag, name string, p *parser) error {
	switch name {
	case "NAMESPACE":
		ss.out(`* NAMESPACE (("" "/")) NIL NIL`)
	case "ENABLE":
		toks, err := p.all()
		if err != nil {
			return err
		}
		var enabled []string
		for _, t := range toks {
			if t.isAtom("CONDSTORE") {
				ss.condstore = true
				enabled = append(enabled, "CONDSTORE")
			}
		}
		ss.out("* ENABLED %s", strings.Join(enabled, " "))
	case "LIST", "XLIST":
		return ss.list(p, name == "XLIST")
	case "LSUB":
		return ss.list(p, false)
	case "STATUS":
		return ss.status(p)
	case "SELECT", "EXAMINE":
		return ss.selectFolder(p, name == "EXAMINE")
	case "CREATE":
		return ss.create(p)
	case "DELETE":
		return ss.deleteFolder(p)
	case "RENAME":
		return ss.rename(p)
	case "SUBSCRIBE", "UNSUBSCRIBE":
	case "APPEND":
		return ss.appendCmd(tag, p)
	case "IDLE":
		return ss.idle()
	case "CLOSE", "UNSELECT":
		if ss.sel == nil {
			return errors.New("no folder selected")
		}
		if name == "CLOSE" && !ss.readOnly {
			ss.expunge(nil)
		}
		ss.sel = nil
	case "FETCH", "STORE", "SEARCH", "COPY", "MOVE", "EXPUNGE":
		return ss.selected(name, false, p)
	case "UID":
		sub, err := p.str()
		if err != nil {
			return err
		}
		return ss.selected(strings.ToUpper(sub), true, p)
	default:
		return fmt.Errorf("unknown command %s", name)
	}
	return nil
}

func (ss *session) selected(name string, uid bool, p *parser) error {
	if ss.sel == nil {
		return errors.New("no folder selected")
	}
	switch name {
	case "FETCH":
		return ss.fetch(p, uid)
	case "STORE":
		return ss.store(p, uid)
	case "SEARCH":
		return ss.search(p, uid)
	case "COPY", "MOVE":
		return ss.copyMove(p, uid, name == "MOVE")
	case "EXPUNGE":
		if !uid {
			ss.expunge(nil)
			return nil
		}
		set, err := p.str()
		if err != nil {
			return err
		}
		ns, err := parseNumSet(set)
		if err != nil {
			return err
		}
		ss.expunge(ns)
		return nil
	}
	return fmt.Errorf("unknown command UID %s", name)
}

func (ss *session) login(tag string, p *parser) error {
	user, err1 := p.str()
	pass, err2 := p.str()
	if err1 != nil || err2 != nil {
		ss.out("%s BAD LOGIN needs user and password", tag)
		return nil
	}
	ok := ss.s.opts.Password != "" && strings.EqualFold(user, ss.s.opts.Account) && pass == ss.s.opts.Password
	ss.finishAuth(tag, ok)
	return nil
}

func (ss *session) finishAuth(tag string, ok bool) {
	if !ok {
		ss.out("%s NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)", tag)
		return
	}
	ss.authd = true
	ss.out("%s OK [CAPABILITY %s] %s authenticated (Success)", tag, ss.caps(), ss.s.opts.Account)
}

func (ss *session) authenticate(tag string, p *parser) error {
	mech, err := p.str()
	if err != nil {
		ss.out("%s BAD AUTHENTICATE needs a mechanism", tag)
		return nil
	}
	ir, irErr := p.str()
	if irErr != nil {
		ss.out("+ ")
		ss.flush()
		line, err := ss.br.ReadString('\n')
		if err != nil {
			return errBye
		}
		ir = strings.TrimRight(line, "\r\n")
	}
	if ir == "*" {
		ss.out("%s BAD authentication cancelled", tag)
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(ir)
	if err != nil {
		ss.out("%s BAD invalid base64", tag)
		return nil
	}
	switch strings.ToUpper(mech) {
	case "PLAIN":
		parts := strings.Split(string(raw), "\x00")
		ok := len(parts) == 3 && ss.s.opts.Password != "" &&
			strings.EqualFold(parts[1], ss.s.opts.Account) && parts[2] == ss.s.opts.Password
		ss.finishAuth(tag, ok)
	case "XOAUTH2":
		user, tok := parseXOAuth2(string(raw))
		ok := false
		if ss.s.opts.Tokens != nil {
			owner, valid := ss.s.opts.Tokens.Validate(tok)
			ok = valid && strings.EqualFold(owner, ss.s.opts.Account) && strings.EqualFold(user, ss.s.opts.Account)
		}
		if !ok {
			// Gmail sends an error challenge first and fails after the client's empty reply.
			ss.out("+ %s", base64.StdEncoding.EncodeToString([]byte(`{"status":"400","schemes":"Bearer","scope":"https://mail.google.com/"}`)))
			ss.flush()
			if _, err := ss.br.ReadString('\n'); err != nil {
				return errBye
			}
		}
		ss.finishAuth(tag, ok)
	default:
		ss.out("%s NO [CANNOT] unsupported mechanism", tag)
	}
	return nil
}

func parseXOAuth2(s string) (user, token string) {
	for _, field := range strings.Split(s, "\x01") {
		k, v, _ := strings.Cut(field, "=")
		switch k {
		case "user":
			user = v
		case "auth":
			token = strings.TrimPrefix(v, "Bearer ")
		}
	}
	return user, token
}

func (ss *session) list(p *parser, xlist bool) error {
	toks, err := p.all()
	if err != nil {
		return err
	}
	if len(toks) > 0 && toks[0].kind == tokList { // selection options, e.g. (SPECIAL-USE)
		toks = toks[1:]
	}
	if len(toks) < 2 {
		return errors.New("LIST needs reference and pattern")
	}
	pattern := toks[1].s
	var statusItems []string
	if len(toks) >= 4 && toks[2].isAtom("RETURN") && toks[3].kind == tokList {
		ret := toks[3].list
		for i := 0; i < len(ret); i++ {
			if ret[i].isAtom("STATUS") && i+1 < len(ret) {
				statusItems = tokenStrings(ret[i+1].list)
				i++
			}
		}
	}
	verb := "LIST"
	if xlist {
		verb = "XLIST"
	}

	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	for _, f := range ss.s.st.folders {
		if !matchPattern(pattern, f.name) {
			continue
		}
		attrs := slices.Clone(f.attrs)
		if xlist {
			attrs = xlistAttrs(f)
		}
		if ss.s.hasChildrenLocked(f.name) {
			attrs = append(attrs, `\HasChildren`)
		} else {
			attrs = append(attrs, `\HasNoChildren`)
		}
		ss.out(`* %s (%s) "/" %s`, verb, strings.Join(attrs, " "), quote(f.name))
		if statusItems != nil && f.match != nil {
			ss.out("* STATUS %s (%s)", quote(f.name), ss.statusItemsLocked(f, statusItems))
		}
	}
	return nil
}

// xlistAttrs are the pre-SPECIAL-USE names Gmail's XLIST uses.
func xlistAttrs(f *folder) []string {
	var out []string
	for _, a := range f.attrs {
		switch a {
		case `\All`:
			a = `\AllMail`
		case `\Junk`:
			a = `\Spam`
		case `\Flagged`:
			a = `\Starred`
		}
		out = append(out, a)
	}
	if f.name == Inbox {
		out = append(out, `\Inbox`)
	}
	return out
}

func (s *Server) hasChildrenLocked(name string) bool {
	for _, f := range s.st.folders {
		if strings.HasPrefix(f.name, name+"/") {
			return true
		}
	}
	return false
}

func matchPattern(pattern, name string) bool {
	if strings.EqualFold(pattern, Inbox) {
		return name == Inbox
	}
	var match func(p, n string) bool
	match = func(p, n string) bool {
		if p == "" {
			return n == ""
		}
		switch p[0] {
		case '*':
			for i := 0; i <= len(n); i++ {
				if match(p[1:], n[i:]) {
					return true
				}
			}
			return false
		case '%':
			for i := 0; i <= len(n); i++ {
				if match(p[1:], n[i:]) {
					return true
				}
				if i < len(n) && n[i] == '/' {
					return false
				}
			}
			return false
		}
		return n != "" && p[0] == n[0] && match(p[1:], n[1:])
	}
	return match(pattern, name)
}

func (ss *session) status(p *parser) error {
	name, err := p.str()
	if err != nil {
		return err
	}
	items, err := p.next()
	if err != nil || items.kind != tokList {
		return errors.New("STATUS needs an item list")
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.s.folderLocked(name)
	if f == nil || f.match == nil {
		return no("[NONEXISTENT] Unknown Mailbox: %s (Failure)", name)
	}
	ss.out("* STATUS %s (%s)", quote(f.name), ss.statusItemsLocked(f, tokenStrings(items.list)))
	return nil
}

func (ss *session) statusItemsLocked(f *folder, items []string) string {
	var out []string
	for _, it := range items {
		switch it = strings.ToUpper(it); it {
		case "MESSAGES":
			out = append(out, fmt.Sprintf("MESSAGES %d", len(f.uids)))
		case "RECENT":
			out = append(out, "RECENT 0")
		case "UIDNEXT":
			out = append(out, fmt.Sprintf("UIDNEXT %d", f.uidNext))
		case "UIDVALIDITY":
			out = append(out, fmt.Sprintf("UIDVALIDITY %d", f.uidv))
		case "UNSEEN":
			n := 0
			for _, uid := range f.uids {
				if !f.byUID[uid].flags[`\Seen`] {
					n++
				}
			}
			out = append(out, fmt.Sprintf("UNSEEN %d", n))
		case "HIGHESTMODSEQ":
			out = append(out, fmt.Sprintf("HIGHESTMODSEQ %d", max(f.modseq, 1)))
		case "MAILBOXID":
			if ss.s.opts.ObjectID {
				out = append(out, fmt.Sprintf("MAILBOXID (%s)", f.id))
			}
		}
	}
	return strings.Join(out, " ")
}

func (ss *session) selectFolder(p *parser, readOnly bool) error {
	name, err := p.str()
	if err != nil {
		return err
	}
	if !p.done() {
		if t, err := p.next(); err == nil && t.kind == tokList {
			for _, o := range t.list {
				if o.isAtom("CONDSTORE") {
					ss.condstore = true
				}
			}
		}
	}
	ss.sel = nil
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.s.folderLocked(name)
	if f == nil || f.match == nil {
		return no("[NONEXISTENT] Unknown Mailbox: %s (Failure)", name)
	}
	ss.sel, ss.readOnly = f, readOnly
	ss.view = slices.Clone(f.uids)
	ss.seenMod = ss.s.st.modseq
	ss.out(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen $NotPhishing $Phishing)`)
	ss.out(`* OK [PERMANENTFLAGS (\Answered \Flagged \Draft \Deleted \Seen $NotPhishing $Phishing \*)] Flags permitted.`)
	ss.out("* OK [UIDVALIDITY %d] UIDs valid.", f.uidv)
	ss.out("* %d EXISTS", len(f.uids))
	ss.out("* 0 RECENT")
	ss.out("* OK [UIDNEXT %d] Predicted next UID.", f.uidNext)
	ss.out("* OK [HIGHESTMODSEQ %d]", max(f.modseq, 1))
	if ss.s.opts.ObjectID {
		ss.out("* OK [MAILBOXID (%s)]", f.id)
	}
	if readOnly {
		ss.out("* OK [READ-ONLY] read-only")
	}
	return nil
}

func (ss *session) create(p *parser) error {
	name, err := p.str()
	if err != nil {
		return err
	}
	name = strings.TrimSuffix(name, "/")
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if ss.s.folderLocked(name) != nil {
		return no("[ALREADYEXISTS] Duplicate folder name %s (Failure)", name)
	}
	if strings.HasPrefix(name, gmailRoot) || strings.HasPrefix(name, `\`) || name == "" {
		return no("[CANNOT] Invalid folder name %s (Failure)", name)
	}
	ss.s.newUserFolder(name)
	ss.s.syncLocked()
	return nil
}

func (ss *session) deleteFolder(p *parser) error {
	name, err := p.str()
	if err != nil {
		return err
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.s.folderLocked(name)
	if f == nil {
		return no("[NONEXISTENT] Unknown Mailbox: %s (Failure)", name)
	}
	if f.system {
		return no("[CANNOT] System folder %s cannot be deleted (Failure)", name)
	}
	for _, m := range ss.s.st.msgs {
		if m.labels[f.name] {
			delete(m.labels, f.name)
			ss.s.touchLocked(m)
		}
	}
	ss.s.st.folders = slices.DeleteFunc(ss.s.st.folders, func(x *folder) bool { return x == f })
	if ss.sel == f {
		ss.sel = nil
	}
	ss.s.syncLocked()
	return nil
}

func (ss *session) rename(p *parser) error {
	from, err1 := p.str()
	to, err2 := p.str()
	if err1 != nil || err2 != nil {
		return errors.New("RENAME needs two names")
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.s.folderLocked(from)
	if f == nil {
		return no("[NONEXISTENT] Unknown Mailbox: %s (Failure)", from)
	}
	if f.system {
		return no("[CANNOT] System folder %s cannot be renamed (Failure)", from)
	}
	if ss.s.folderLocked(to) != nil {
		return no("[ALREADYEXISTS] Duplicate folder name %s (Failure)", to)
	}
	for _, m := range ss.s.st.msgs {
		if m.labels[from] {
			delete(m.labels, from)
			m.labels[to] = true
			ss.s.touchLocked(m)
		}
	}
	f.name = to
	f.match = func(m *Message) bool { return m.labels[to] && !m.trashOrSpam() }
	f.add = func(m *Message) { m.labels[to] = true }
	f.remove = func(_ *Server, m *Message) { delete(m.labels, to) }
	ss.s.syncLocked()
	return nil
}

func (ss *session) appendCmd(tag string, p *parser) error {
	name, err := p.str()
	if err != nil {
		return err
	}
	var flags []string
	date := time.Now()
	var msg string
	for {
		t, err := p.next()
		if err != nil {
			return errors.New("APPEND needs a message literal")
		}
		if t.kind == tokList {
			flags = tokenStrings(t.list)
			continue
		}
		if t.kind == tokString && p.done() {
			msg = t.s
			break
		}
		if t.kind == tokString {
			if d, err := time.Parse("02-Jan-2006 15:04:05 -0700", strings.TrimSpace(t.s)); err == nil {
				date = d
			}
			continue
		}
		return fmt.Errorf("unexpected %q", t.s)
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.s.folderLocked(name)
	if f == nil || f.match == nil {
		return no("[TRYCREATE] Folder doesn't exist. (Failure)")
	}
	m := ss.s.insertLocked(f, []byte(msg), flags, date)
	ss.s.syncLocked()
	ss.pushUpdatesLocked()
	ss.out("%s OK [APPENDUID %d %d] (Success)", tag, f.uidv, f.uidOf[m])
	return errAnswered
}

// errAnswered means the handler already wrote the tagged reply.
var errAnswered = errors.New("answered")

func (ss *session) idle() error {
	ss.out("+ idling")
	ss.flush()
	lines := make(chan string, 1)
	go func() {
		line, err := ss.br.ReadString('\n')
		if err != nil {
			line = ""
		}
		lines <- strings.TrimRight(line, "\r\n")
	}()
	for {
		ss.s.mu.Lock()
		changed := ss.s.st.changed
		ss.s.mu.Unlock()
		select {
		case line := <-lines:
			if !strings.EqualFold(line, "DONE") {
				return errBye
			}
			return nil
		case <-changed:
			ss.pushUpdates()
			ss.flush()
		}
	}
}

// pushUpdates tells the client about expunges, new messages and flag changes in
// the selected folder since it last heard.
func (ss *session) pushUpdates() {
	if ss.sel == nil {
		return
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	ss.pushUpdatesLocked()
}

func (ss *session) pushUpdatesLocked() {
	f := ss.sel
	if f == nil {
		return
	}
	for i := len(ss.view) - 1; i >= 0; i-- {
		if _, ok := f.byUID[ss.view[i]]; !ok {
			ss.out("* %d EXPUNGE", i+1)
			ss.view = slices.Delete(ss.view, i, i+1)
		}
	}
	for i, uid := range ss.view {
		if m := f.byUID[uid]; m.ModSeq > ss.seenMod {
			ss.out("* %d FETCH (%s)", i+1, ss.flagItems(f, m))
		}
	}
	known := len(ss.view)
	for _, uid := range f.uids {
		if len(ss.view) == 0 || uid > ss.view[len(ss.view)-1] {
			ss.view = append(ss.view, uid)
		}
	}
	if len(ss.view) != known {
		ss.out("* %d EXISTS", len(ss.view))
	}
	ss.seenMod = ss.s.st.modseq
}

func (ss *session) flagItems(f *folder, m *Message) string {
	items := fmt.Sprintf("UID %d FLAGS (%s) X-GM-LABELS (%s)", f.uidOf[m], strings.Join(m.Flags(), " "), labelList(m))
	if ss.condstore {
		items += fmt.Sprintf(" MODSEQ (%d)", m.ModSeq)
	}
	return items
}

func labelList(m *Message) string {
	ls := m.Labels()
	for i, l := range ls {
		ls[i] = quote(l)
	}
	return strings.Join(ls, " ")
}
