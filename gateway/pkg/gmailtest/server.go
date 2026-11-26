package gmailtest

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// TokenValidator checks OAuth access tokens; *oauthtest.Server implements it.
type TokenValidator interface {
	Validate(accessToken string) (user string, ok bool)
}

// Options configure a Server.
type Options struct {
	// Account is the Gmail address; LOGIN and XOAUTH2 must name it. Default "user@gmail.test".
	Account string
	// Password enables LOGIN and AUTHENTICATE PLAIN (an app password). Empty disables them.
	Password string
	// Tokens enables AUTHENTICATE XOAUTH2. Nil rejects every token.
	Tokens TokenValidator
	// ObjectID adds RFC 8474 OBJECTID, which real Gmail does not have.
	ObjectID bool
}

// Server is a fake Gmail IMAP server for one account.
type Server struct {
	opts Options
	ln   net.Listener

	mu        sync.Mutex
	intercept func(command string) string
	st        store
	conns     map[net.Conn]struct{}
	wg        sync.WaitGroup
}

// New starts a server on a loopback port; it stops when the test ends.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.Account == "" {
		opts.Account = "user@gmail.test"
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{opts: opts, ln: ln, conns: map[net.Conn]struct{}{}}
	s.initStore()
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

// Addr is the host:port to dial (plaintext; the gateway's TLS settings must allow it).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Account is the account's address.
func (s *Server) Account() string { return s.opts.Account }

// Close stops the listener and drops every connection.
func (s *Server) Close() {
	s.ln.Close()
	s.DropConnections()
	s.wg.Wait()
}

// DropConnections closes every client connection without BYE, like a network failure.
func (s *Server) DropConnections() {
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			newSession(s, c).serve()
			c.Close()
			s.mu.Lock()
			delete(s.conns, c)
			s.mu.Unlock()
		}()
	}
}

// ErrNoFolder is returned for an unknown folder name.
var ErrNoFolder = errors.New("gmailtest: no such folder")

// Add stores raw as if APPENDed to folder, with IMAP flags such as `\Seen` or `\Draft`.
// It returns the message and its UID in that folder.
func (s *Server) Add(folderName string, raw []byte, flags ...string) (*Message, uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.folderLocked(folderName)
	if f == nil || f.match == nil {
		return nil, 0, fmt.Errorf("%w: %q", ErrNoFolder, folderName)
	}
	m := s.insertLocked(f, raw, flags, time.Now())
	s.syncLocked()
	return m, f.uidOf[m], nil
}

// MustAdd is Add that panics on an unknown folder; for fixtures.
func (s *Server) MustAdd(folderName string, raw []byte, flags ...string) (*Message, uint32) {
	m, uid, err := s.Add(folderName, raw, flags...)
	if err != nil {
		panic(err)
	}
	return m, uid
}

// SetLabels adds and removes labels on a message, as the Gmail web UI would.
// Flag-backed labels (\Starred, \Draft) work too. Folders for new user labels are created.
func (s *Server) SetLabels(m *Message, add, remove []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range add {
		setLabel(m, l, true)
		s.ensureLabelFolderLocked(canonicalLabel(l))
	}
	for _, l := range remove {
		setLabel(m, l, false)
	}
	s.touchLocked(m)
	s.syncLocked()
}

// SetFlags adds and removes IMAP flags on a message.
func (s *Server) SetFlags(m *Message, add, remove []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range add {
		setFlag(m, canonicalFlag(f), true)
	}
	for _, f := range remove {
		setFlag(m, canonicalFlag(f), false)
	}
	s.touchLocked(m)
	s.syncLocked()
}

// Delete removes a message from the account permanently.
func (s *Server) Delete(m *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(m)
	s.syncLocked()
}

// UID returns the message's UID in folder, or 0 if it is not there.
func (s *Server) UID(folderName string, m *Message) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.folderLocked(folderName); f != nil && f.uidOf != nil {
		return f.uidOf[m]
	}
	return 0
}

// At returns the message with uid in folder, or nil.
func (s *Server) At(folderName string, uid uint32) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.folderLocked(folderName); f != nil && f.byUID != nil {
		return f.byUID[uid]
	}
	return nil
}

// ByGmMsgID returns the message with that X-GM-MSGID, or nil.
func (s *Server) ByGmMsgID(id uint64) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.st.msgs {
		if m.GmMsgID == id {
			return m
		}
	}
	return nil
}

// LabelsOf returns X-GM-LABELS of m under the lock, safe while sessions run.
func (s *Server) LabelsOf(m *Message) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return m.Labels()
}

// FlagsOf returns the IMAP flags of m under the lock.
func (s *Server) FlagsOf(m *Message) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return m.Flags()
}

// UIDValidity returns a folder's UIDVALIDITY, or 0 for an unknown folder.
func (s *Server) UIDValidity(folderName string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.folderLocked(folderName); f != nil {
		return f.uidv
	}
	return 0
}

// CreateLabel creates a user label (folder).
func (s *Server) CreateLabel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLabelFolderLocked(name)
	s.syncLocked()
}

// ResetUIDValidity renumbers a folder under a new UIDVALIDITY, as Gmail occasionally
// does; clients must drop what they cached for it.
func (s *Server) ResetUIDValidity(folderName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.folderLocked(folderName)
	if f == nil || f.match == nil {
		return fmt.Errorf("%w: %q", ErrNoFolder, folderName)
	}
	s.newUIDValidity(f)
	s.syncLocked()
	return nil
}

func (s *Server) ensureLabelFolderLocked(label string) {
	switch label {
	case LabelInbox, LabelSent, LabelImportant, LabelTrash, LabelSpam, LabelStarred, LabelDraft:
		return
	}
	if s.folderLocked(label) == nil {
		s.newUserFolder(label)
	}
}

// SetIntercept installs fn to see every command line after authentication (tag
// stripped, literals inlined). A non-empty return ends that command with it instead
// of running it, e.g. "NO [UNAVAILABLE] Temporary System Error"; "BYE" drops the
// connection. nil removes it.
func (s *Server) SetIntercept(fn func(command string) string) {
	s.mu.Lock()
	s.intercept = fn
	s.mu.Unlock()
}
