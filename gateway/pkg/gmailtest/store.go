package gmailtest

import (
	"bytes"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// System labels as X-GM-LABELS shows them.
const (
	LabelInbox     = `\Inbox`
	LabelSent      = `\Sent`
	LabelImportant = `\Important`
	LabelTrash     = `\Trash`
	LabelSpam      = `\Spam`
	// LabelStarred and LabelDraft are the \Flagged and \Draft message flags seen as labels.
	LabelStarred = `\Starred`
	LabelDraft   = `\Draft`
)

// Folder names of the Gmail system folders (English UI).
const (
	Inbox     = "INBOX"
	AllMail   = "[Gmail]/All Mail"
	Drafts    = "[Gmail]/Drafts"
	Important = "[Gmail]/Important"
	SentMail  = "[Gmail]/Sent Mail"
	Spam      = "[Gmail]/Spam"
	Starred   = "[Gmail]/Starred"
	Trash     = "[Gmail]/Trash"
	gmailRoot = "[Gmail]"
)

// Message is one message in the account. Its exported fields are read-only for
// tests; change messages through Server methods so folders and IDLE stay in sync.
type Message struct {
	GmMsgID      uint64
	GmThrID      uint64
	Raw          []byte
	InternalDate time.Time
	ModSeq       uint64

	flags  map[string]bool // IMAP flags; \Flagged and \Draft double as \Starred and \Draft labels
	labels map[string]bool // labels other than \Starred and \Draft
	msgID  string          // Message-ID header, for threading
	seq    int             // arrival order
}

// Flags returns the message's IMAP flags, sorted.
func (m *Message) Flags() []string { return sortedKeys(m.flags) }

// Labels returns X-GM-LABELS as Gmail shows them, sorted.
func (m *Message) Labels() []string {
	out := sortedKeys(m.labels)
	if m.flags[`\Flagged`] {
		out = append(out, LabelStarred)
	}
	if m.flags[`\Draft`] {
		out = append(out, LabelDraft)
	}
	slices.Sort(out)
	return out
}

func (m *Message) trashOrSpam() bool { return m.labels[LabelTrash] || m.labels[LabelSpam] }

// folder is a view over the labels with its own UID numbering.
type folder struct {
	name    string
	attrs   []string // special-use attributes
	id      string   // MAILBOXID
	system  bool
	match   func(*Message) bool
	add     func(*Message)          // COPY / APPEND into the folder
	remove  func(*Server, *Message) // MOVE out of / EXPUNGE from the folder
	uidv    uint32
	uidNext uint32
	uids    []uint32 // ascending
	byUID   map[uint32]*Message
	uidOf   map[*Message]uint32
	modseq  uint64 // highest modseq of anything that happened in this folder
}

func (f *folder) reset(uidv uint32) {
	f.uidv = uidv
	f.uidNext = 1
	f.uids = nil
	f.byUID = map[uint32]*Message{}
	f.uidOf = map[*Message]uint32{}
}

// store is the account state, guarded by Server.mu.
type store struct {
	msgs     []*Message
	folders  []*folder
	modseq   uint64
	nextGmID uint64
	nextUIDV uint32
	arrivals int
	changed  chan struct{} // closed and replaced on every change
}

func (s *Server) initStore() {
	s.st = store{
		modseq:   1,
		nextGmID: 1795000000000000001,
		nextUIDV: 1,
		changed:  make(chan struct{}),
	}
	labelFolder := func(name, label string, attrs ...string) {
		s.st.folders = append(s.st.folders, &folder{
			name: name, attrs: attrs, system: true,
			match:  func(m *Message) bool { return m.labels[label] && !m.trashOrSpam() },
			add:    func(m *Message) { m.labels[label] = true },
			remove: func(_ *Server, m *Message) { delete(m.labels, label) },
		})
	}
	labelFolder(Inbox, LabelInbox)
	s.st.folders = append(s.st.folders,
		&folder{name: gmailRoot, attrs: []string{`\Noselect`}, system: true},
		&folder{
			name: AllMail, attrs: []string{`\All`}, system: true,
			match:  func(m *Message) bool { return !m.trashOrSpam() },
			add:    func(*Message) {},
			remove: func(_ *Server, m *Message) { trash(m) },
		},
		&folder{
			name: Drafts, attrs: []string{`\Drafts`}, system: true,
			match:  func(m *Message) bool { return m.flags[`\Draft`] && !m.labels[LabelTrash] },
			add:    func(m *Message) { m.flags[`\Draft`] = true },
			remove: func(_ *Server, m *Message) { delete(m.flags, `\Draft`) },
		})
	labelFolder(Important, LabelImportant, `\Important`)
	labelFolder(SentMail, LabelSent, `\Sent`)
	s.st.folders = append(s.st.folders,
		&folder{
			name: Spam, attrs: []string{`\Junk`}, system: true,
			match:  func(m *Message) bool { return m.labels[LabelSpam] },
			add:    func(m *Message) { m.labels[LabelSpam] = true; delete(m.labels, LabelTrash) },
			remove: func(s *Server, m *Message) { s.deleteLocked(m) },
		},
		&folder{
			name: Starred, attrs: []string{`\Flagged`}, system: true,
			match:  func(m *Message) bool { return m.flags[`\Flagged`] && !m.trashOrSpam() },
			add:    func(m *Message) { m.flags[`\Flagged`] = true },
			remove: func(_ *Server, m *Message) { delete(m.flags, `\Flagged`) },
		},
		&folder{
			name: Trash, attrs: []string{`\Trash`}, system: true,
			match:  func(m *Message) bool { return m.labels[LabelTrash] },
			add:    trash,
			remove: func(s *Server, m *Message) { s.deleteLocked(m) },
		})
	for _, f := range s.st.folders {
		s.newUIDValidity(f)
	}
}

func trash(m *Message) {
	m.labels[LabelTrash] = true
	delete(m.labels, LabelSpam)
}

func (s *Server) newUIDValidity(f *folder) {
	f.reset(s.st.nextUIDV)
	s.st.nextUIDV++
	f.id = fmt.Sprintf("F%08x", f.uidv)
}

func (s *Server) newUserFolder(name string) *folder {
	f := &folder{
		name:   name,
		match:  func(m *Message) bool { return m.labels[name] && !m.trashOrSpam() },
		add:    func(m *Message) { m.labels[name] = true },
		remove: func(_ *Server, m *Message) { delete(m.labels, name) },
	}
	s.newUIDValidity(f)
	s.st.folders = append(s.st.folders, f)
	return f
}

func (s *Server) folderLocked(name string) *folder {
	if strings.EqualFold(name, Inbox) {
		name = Inbox
	}
	for _, f := range s.st.folders {
		if f.name == name {
			return f
		}
	}
	return nil
}

// insertLocked adds a message (APPEND, Gmail API send) into folder f.
func (s *Server) insertLocked(f *folder, raw []byte, flags []string, date time.Time) *Message {
	m := &Message{
		GmMsgID:      s.st.nextGmID,
		Raw:          append([]byte(nil), raw...),
		InternalDate: date,
		flags:        map[string]bool{},
		labels:       map[string]bool{},
		seq:          s.st.arrivals,
	}
	s.st.nextGmID++
	s.st.arrivals++
	for _, fl := range flags {
		if !strings.EqualFold(fl, `\Recent`) {
			m.flags[canonicalFlag(fl)] = true
		}
	}
	m.GmThrID = s.threadFor(m)
	f.add(m)
	s.st.msgs = append(s.st.msgs, m)
	s.touchLocked(m)
	return m
}

// threadFor joins m to the thread of a message it references, Gmail-style.
func (s *Server) threadFor(m *Message) uint64 {
	h, err := mail.ReadMessage(bytes.NewReader(m.Raw))
	if err != nil {
		return m.GmMsgID
	}
	m.msgID = strings.TrimSpace(h.Header.Get("Message-Id"))
	refs := strings.Fields(h.Header.Get("References") + " " + h.Header.Get("In-Reply-To"))
	for _, ref := range refs {
		for _, other := range s.st.msgs {
			if other.msgID != "" && other.msgID == ref {
				return other.GmThrID
			}
		}
	}
	return m.GmMsgID
}

func (s *Server) deleteLocked(m *Message) {
	s.st.msgs = slices.DeleteFunc(s.st.msgs, func(x *Message) bool { return x == m })
	s.st.modseq++
	m.ModSeq = s.st.modseq
}

// touchLocked records that m changed.
func (s *Server) touchLocked(m *Message) {
	s.st.modseq++
	m.ModSeq = s.st.modseq
}

// syncLocked brings every folder's UID list in line with the labels, assigning new
// UIDs to messages that joined a folder, and wakes IDLE sessions.
func (s *Server) syncLocked() {
	alive := make(map[*Message]bool, len(s.st.msgs))
	for _, m := range s.st.msgs {
		alive[m] = true
	}
	for _, f := range s.st.folders {
		if f.match == nil {
			continue
		}
		var keep []uint32
		for _, uid := range f.uids {
			m := f.byUID[uid]
			if alive[m] && f.match(m) {
				keep = append(keep, uid)
				f.modseq = max(f.modseq, m.ModSeq)
				continue
			}
			delete(f.byUID, uid)
			delete(f.uidOf, m)
			s.st.modseq++
			f.modseq = s.st.modseq
		}
		f.uids = keep
		for _, m := range s.st.msgs { // arrival order
			if _, in := f.uidOf[m]; in || !f.match(m) {
				continue
			}
			uid := f.uidNext
			f.uidNext++
			f.uids = append(f.uids, uid)
			f.byUID[uid] = m
			f.uidOf[m] = uid
			f.modseq = max(f.modseq, m.ModSeq)
		}
	}
	close(s.st.changed)
	s.st.changed = make(chan struct{})
}

func canonicalFlag(f string) string {
	for _, sys := range []string{`\Seen`, `\Answered`, `\Flagged`, `\Deleted`, `\Draft`} {
		if strings.EqualFold(f, sys) {
			return sys
		}
	}
	return f
}

// canonicalLabel maps the system labels case-insensitively, as Gmail does.
func canonicalLabel(l string) string {
	for _, sys := range []string{LabelInbox, LabelSent, LabelImportant, LabelTrash, LabelSpam, LabelStarred, LabelDraft} {
		if strings.EqualFold(l, sys) {
			return sys
		}
	}
	if strings.EqualFold(l, Inbox) {
		return LabelInbox
	}
	return l
}

// setLabel adds or removes one label, including the flag-backed ones.
func setLabel(m *Message, label string, on bool) {
	switch label = canonicalLabel(label); label {
	case LabelStarred:
		setFlag(m, `\Flagged`, on)
	case LabelDraft:
		setFlag(m, `\Draft`, on)
	default:
		if on {
			m.labels[label] = true
		} else {
			delete(m.labels, label)
		}
	}
}

func setFlag(m *Message, flag string, on bool) {
	if on {
		m.flags[flag] = true
	} else {
		delete(m.flags, flag)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
