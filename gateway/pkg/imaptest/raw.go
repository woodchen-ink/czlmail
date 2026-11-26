package imaptest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Raw is a line-level IMAP client for checking exact server replies, including
// extensions imapclient does not parse (X-GM-*, OBJECTID, NOTIFY). Literals in
// replies are inlined into the response line. It is closed when the test ends.
type Raw struct {
	t        testing.TB
	conn     net.Conn
	r        *bufio.Reader
	tag      int
	Greeting string
	// Timeout bounds every read; zero means 10 seconds.
	Timeout time.Duration
}

// Reply is everything a server sent for one command.
type Reply struct {
	Untagged []string // "* ..." lines without the leading "* ", and "+ ..." lines as sent
	Status   string   // tagged line without the tag, e.g. "OK [APPENDUID 7 3] done"
}

// OK reports whether the command completed with OK.
func (r Reply) OK() bool { return strings.HasPrefix(r.Status, "OK") }

// Find returns the first untagged response containing substr, or "".
func (r Reply) Find(substr string) string {
	for _, u := range r.Untagged {
		if strings.Contains(u, substr) {
			return u
		}
	}
	return ""
}

// DialRaw connects to addr and reads the greeting.
func DialRaw(t testing.TB, addr string) *Raw {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("imaptest: dial %s: %v", addr, err)
	}
	c := &Raw{t: t, conn: conn, r: bufio.NewReader(conn)}
	t.Cleanup(func() { conn.Close() })
	g, err := c.ReadResponse()
	if err != nil {
		t.Fatalf("imaptest: greeting from %s: %v", addr, err)
	}
	c.Greeting = g
	return c
}

// Cmd sends one command (without tag) and reads until its tagged reply.
func (c *Raw) Cmd(format string, args ...any) Reply {
	c.t.Helper()
	tag := c.nextTag()
	c.Send(tag + " " + fmt.Sprintf(format, args...))
	return c.readUntil(tag)
}

// MustOK is Cmd that fails the test unless the reply is OK.
func (c *Raw) MustOK(format string, args ...any) Reply {
	c.t.Helper()
	rep := c.Cmd(format, args...)
	if !rep.OK() {
		c.t.Fatalf("imaptest: %s: %s", fmt.Sprintf(format, args...), rep.Status)
	}
	return rep
}

// CmdLiteral sends prefix followed by data as a synchronising literal, then suffix,
// e.g. CmdLiteral(`APPEND INBOX (\Seen)`, raw, "").
func (c *Raw) CmdLiteral(prefix string, data []byte, suffix string) Reply {
	c.t.Helper()
	tag := c.nextTag()
	c.Send(fmt.Sprintf("%s %s {%d}", tag, prefix, len(data)))
	line, err := c.ReadResponse()
	if err != nil {
		c.t.Fatalf("imaptest: %s: %v", prefix, err)
	}
	if !strings.HasPrefix(line, "+") {
		return Reply{Status: strings.TrimPrefix(line, tag+" ")}
	}
	buf := append(append([]byte{}, data...), []byte(suffix+"\r\n")...)
	c.write(buf)
	return c.readUntil(tag)
}

// Send writes one line; CRLF is added.
func (c *Raw) Send(line string) {
	c.t.Helper()
	c.write([]byte(line + "\r\n"))
}

func (c *Raw) write(b []byte) {
	c.t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout()))
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("imaptest: write: %v", err)
	}
}

var literalSuffix = regexp.MustCompile(`\{(\d+)\+?\}$`)

// ReadResponse reads one server response with literals inlined, without the final CRLF.
func (c *Raw) ReadResponse() (string, error) {
	return c.ReadResponseWithin(c.timeout())
}

// ReadResponseWithin is ReadResponse with its own timeout, for waiting on IDLE pushes.
func (c *Raw) ReadResponseWithin(d time.Duration) (string, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
	var out strings.Builder
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return out.String() + line, err
		}
		line = strings.TrimRight(line, "\r\n")
		out.WriteString(line)
		m := literalSuffix.FindStringSubmatch(line)
		if m == nil {
			return out.String(), nil
		}
		n, _ := strconv.Atoi(m[1])
		out.WriteString("\r\n")
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return out.String(), err
		}
		out.Write(buf)
	}
}

func (c *Raw) readUntil(tag string) Reply {
	c.t.Helper()
	var rep Reply
	for {
		line, err := c.ReadResponse()
		if err != nil {
			c.t.Fatalf("imaptest: waiting for %s: %v (got %q)", tag, err, rep.Untagged)
		}
		switch {
		case strings.HasPrefix(line, tag+" "):
			rep.Status = strings.TrimPrefix(line, tag+" ")
			return rep
		case strings.HasPrefix(line, "* "):
			rep.Untagged = append(rep.Untagged, line[2:])
		default:
			rep.Untagged = append(rep.Untagged, line)
		}
	}
}

func (c *Raw) nextTag() string {
	c.tag++
	return "t" + strconv.Itoa(c.tag)
}

func (c *Raw) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 10 * time.Second
}
