package imaptest

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Mailbox is a signed-in IMAP session that sets up mailbox state for a test.
// Failures end the test. It is closed when the test ends.
type Mailbox struct {
	t testing.TB
	C *imapclient.Client
}

// Dial signs u in. The servers may still be starting when CI begins, so it retries
// for a few seconds before failing.
func (u *User) Dial(t testing.TB) *Mailbox {
	t.Helper()
	var c *imapclient.Client
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err = dial(u.Server.Addr)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("imaptest: connect %s: %v", u.Server.Addr, err)
	}
	if err := c.Login(u.Name, u.Password).Wait(); err != nil {
		c.Close()
		t.Fatalf("imaptest: login %s: %v", u.Name, err)
	}
	t.Cleanup(func() {
		_ = c.Logout().Wait()
		c.Close()
	})
	return &Mailbox{t: t, C: c}
}

func dial(addr string) (*imapclient.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	c := imapclient.New(conn, nil)
	if err := c.WaitGreeting(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Create creates a mailbox (folder).
func (m *Mailbox) Create(name string) {
	m.t.Helper()
	if err := m.C.Create(name, nil).Wait(); err != nil {
		m.t.Fatalf("imaptest: create %q: %v", name, err)
	}
}

// Delete deletes a mailbox and the messages in it.
func (m *Mailbox) Delete(name string) {
	m.t.Helper()
	m.unselect()
	if err := m.C.Delete(name).Wait(); err != nil {
		m.t.Fatalf("imaptest: delete %q: %v", name, err)
	}
}

// Recreate deletes the mailbox and creates one with the same name, which gives it a
// new UIDVALIDITY. It returns the old and new UIDVALIDITY.
func (m *Mailbox) Recreate(name string) (before, after uint32) {
	m.t.Helper()
	before = m.UIDValidity(name)
	m.Delete(name)
	m.Create(name)
	after = m.UIDValidity(name)
	if after == before {
		m.t.Fatalf("imaptest: recreate %q kept UIDVALIDITY %d", name, before)
	}
	return before, after
}

// UIDValidity returns the mailbox's UIDVALIDITY.
func (m *Mailbox) UIDValidity(name string) uint32 {
	m.t.Helper()
	st, err := m.C.Status(name, &imap.StatusOptions{UIDValidity: true}).Wait()
	if err != nil {
		m.t.Fatalf("imaptest: status %q: %v", name, err)
	}
	return st.UIDValidity
}

// Status returns message count, UIDNEXT and UIDVALIDITY.
func (m *Mailbox) Status(name string) *imap.StatusData {
	m.t.Helper()
	st, err := m.C.Status(name, &imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true, NumUnseen: true}).Wait()
	if err != nil {
		m.t.Fatalf("imaptest: status %q: %v", name, err)
	}
	return st
}

// Append stores raw in the mailbox with the given flags and returns its UID.
func (m *Mailbox) Append(name string, raw []byte, flags ...imap.Flag) imap.UID {
	m.t.Helper()
	cmd := m.C.Append(name, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: time.Now()})
	if _, err := cmd.Write(raw); err != nil {
		m.t.Fatalf("imaptest: append %q: %v", name, err)
	}
	if err := cmd.Close(); err != nil {
		m.t.Fatalf("imaptest: append %q: %v", name, err)
	}
	data, err := cmd.Wait()
	if err != nil {
		m.t.Fatalf("imaptest: append %q: %v", name, err)
	}
	return data.UID
}

// AppendSample appends Sample(subject) and returns its UID.
func (m *Mailbox) AppendSample(name, subject string, flags ...imap.Flag) imap.UID {
	m.t.Helper()
	return m.Append(name, Sample(subject), flags...)
}

// AddFlags adds flags to one message.
func (m *Mailbox) AddFlags(name string, uid imap.UID, flags ...imap.Flag) {
	m.t.Helper()
	m.store(name, uid, imap.StoreFlagsAdd, flags)
}

// RemoveFlags removes flags from one message.
func (m *Mailbox) RemoveFlags(name string, uid imap.UID, flags ...imap.Flag) {
	m.t.Helper()
	m.store(name, uid, imap.StoreFlagsDel, flags)
}

func (m *Mailbox) store(name string, uid imap.UID, op imap.StoreFlagsOp, flags []imap.Flag) {
	m.t.Helper()
	m.selectBox(name)
	err := m.C.Store(imap.UIDSetNum(uid), &imap.StoreFlags{Op: op, Silent: true, Flags: flags}, nil).Close()
	if err != nil {
		m.t.Fatalf("imaptest: store %q uid %d: %v", name, uid, err)
	}
}

// Move moves one message and returns its UID in the destination.
func (m *Mailbox) Move(from string, uid imap.UID, to string) imap.UID {
	m.t.Helper()
	m.selectBox(from)
	data, err := m.C.Move(imap.UIDSetNum(uid), to).Wait()
	if err != nil {
		m.t.Fatalf("imaptest: move %q uid %d to %q: %v", from, uid, to, err)
	}
	set, ok := data.DestUIDs.(imap.UIDSet)
	if !ok {
		m.t.Fatalf("imaptest: move %q uid %d: no COPYUID", from, uid)
	}
	uids, _ := set.Nums()
	if len(uids) != 1 {
		m.t.Fatalf("imaptest: move %q uid %d: COPYUID %v", from, uid, set)
	}
	return uids[0]
}

// Expunge permanently removes one message.
func (m *Mailbox) Expunge(name string, uid imap.UID) {
	m.t.Helper()
	m.store(name, uid, imap.StoreFlagsAdd, []imap.Flag{imap.FlagDeleted})
	if err := m.C.UIDExpunge(imap.UIDSetNum(uid)).Close(); err != nil {
		m.t.Fatalf("imaptest: expunge %q uid %d: %v", name, uid, err)
	}
}

// UIDs returns the UIDs in the mailbox, ascending.
func (m *Mailbox) UIDs(name string) []imap.UID {
	m.t.Helper()
	m.selectBox(name)
	data, err := m.C.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		m.t.Fatalf("imaptest: search %q: %v", name, err)
	}
	return data.AllUIDs()
}

// Flags returns one message's flags.
func (m *Mailbox) Flags(name string, uid imap.UID) []imap.Flag {
	m.t.Helper()
	m.selectBox(name)
	msgs, err := m.C.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		m.t.Fatalf("imaptest: fetch %q uid %d: %v", name, uid, err)
	}
	if len(msgs) != 1 {
		m.t.Fatalf("imaptest: fetch %q uid %d: %d messages", name, uid, len(msgs))
	}
	return msgs[0].Flags
}

func (m *Mailbox) selectBox(name string) {
	m.t.Helper()
	if mb := m.C.Mailbox(); mb != nil && mb.Name == name {
		return
	}
	if _, err := m.C.Select(name, nil).Wait(); err != nil {
		m.t.Fatalf("imaptest: select %q: %v", name, err)
	}
}

func (m *Mailbox) unselect() {
	if m.C.Mailbox() != nil {
		_ = m.C.Unselect().Wait()
	}
}

// Sample returns a small RFC 5322 message with a unique Message-ID.
func Sample(subject string) []byte {
	return SampleFrom("sender@example.test", "rcpt@example.test", subject)
}

// SampleFrom is Sample with explicit addresses.
func SampleFrom(from, to, subject string) []byte {
	var b bytes.Buffer
	now := time.Now().UTC()
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%d.%s@imaptest.invalid>\r\n", now.UnixNano(), randHex(4))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	fmt.Fprintf(&b, "Body of %s.\r\n", subject)
	return b.Bytes()
}
