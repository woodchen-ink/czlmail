// Package imapc is the gateway's IMAP4rev1 client. It reads every response into a
// generic value tree (see Value) before interpreting it, so the extensions the
// gateway depends on — CONDSTORE, QRESYNC (VANISHED), OBJECTID, X-GUID, X-GM-*,
// NOTIFY, LIST-STATUS — work, and unknown data never breaks the connection.
//
// Commands run one at a time; a Client is safe for use by one goroutine at a time
// plus Close from any goroutine.
package imapc

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TLS modes of a connection.
const (
	TLSImplicit = "tls"      // TLS from the first byte (port 993)
	TLSStartTLS = "starttls" // plaintext, then STARTTLS (required)
	TLSNone     = "plain"    // no TLS; only for tests and trusted networks
)

// Options configure Dial.
type Options struct {
	TLS       string // TLSImplicit, TLSStartTLS or TLSNone
	TLSConfig *tls.Config
	// Timeout bounds each command's wait for the server (not IDLE). Default 2 min.
	Timeout time.Duration
	// Unsolicited receives untagged responses that arrive while no command is
	// waiting for them, and the EXISTS/EXPUNGE/FETCH/VANISHED of any command.
	// It runs on the reading goroutine and must not call the Client.
	Unsolicited func(*Response)
	// OnCommand, if set, is called before each command is sent (not for IDLE's
	// continuation); the gateway counts upstream requests with it.
	OnCommand func(name string)
}

// Client is one IMAP connection.
type Client struct {
	conn net.Conn
	r    *reader
	bw   *bufio.Writer
	opts Options

	mu      sync.Mutex // one command at a time
	tagN    int
	caps    map[string]bool
	enabled map[string]bool

	closeOnce sync.Once
	closed    chan struct{}

	selected *Mailbox
}

// StatusError is a NO, BAD or BYE answer.
type StatusError struct {
	Command string
	Status  string // NO, BAD, BYE
	Code    string // response code, e.g. AUTHENTICATIONFAILED, LIMIT, TRYCREATE
	Text    string
}

func (e *StatusError) Error() string {
	code := ""
	if e.Code != "" {
		code = "[" + e.Code + "] "
	}
	return fmt.Sprintf("imap %s: %s %s%s", e.Command, e.Status, code, e.Text)
}

// ErrClosed is returned once the connection is gone.
var ErrClosed = errors.New("imapc: connection closed")

// Dial connects with dial (the upstream's netx.Dialer), reads the greeting and,
// for TLSStartTLS, upgrades. addr is host:port; the host is the TLS server name.
func Dial(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), addr string, opts Options) (*Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	tc := opts.TLSConfig
	if tc == nil {
		tc = &tls.Config{}
	}
	tc = tc.Clone()
	if tc.ServerName == "" {
		tc.ServerName = host
	}
	if opts.TLS == TLSImplicit {
		t := tls.Client(conn, tc)
		if err := handshake(ctx, t); err != nil {
			conn.Close()
			return nil, err
		}
		conn = t
	}
	c := newClient(conn, opts)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(opts.Timeout))
	greet, err := c.r.readResponse()
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	if greet.Type == "BYE" {
		conn.Close()
		return nil, &StatusError{Command: "greeting", Status: "BYE", Code: greet.Code, Text: greet.Text}
	}
	if greet.Code == "CAPABILITY" {
		c.setCaps(greet.CodeArgs)
	}
	if opts.TLS == TLSStartTLS {
		if err := c.startTLS(ctx, tc); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return c, nil
}

func handshake(ctx context.Context, t *tls.Conn) error {
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return t.HandshakeContext(hctx)
}

func newClient(conn net.Conn, opts Options) *Client {
	return &Client{
		conn:    conn,
		r:       &reader{br: bufio.NewReaderSize(conn, 64<<10)},
		bw:      bufio.NewWriterSize(conn, 16<<10),
		opts:    opts,
		enabled: map[string]bool{},
		closed:  make(chan struct{}),
	}
}

func (c *Client) startTLS(ctx context.Context, tc *tls.Config) error {
	if _, err := c.Execute(ctx, "STARTTLS", nil); err != nil {
		return err
	}
	t := tls.Client(c.conn, tc)
	if err := handshake(ctx, t); err != nil {
		return err
	}
	c.conn = t
	c.r = &reader{br: bufio.NewReaderSize(t, 64<<10)}
	c.bw = bufio.NewWriterSize(t, 16<<10)
	c.caps = nil // must be re-read after STARTTLS
	return nil
}

// Close drops the connection without LOGOUT. Safe from any goroutine.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.conn.Close()
}

// Done is closed by Close.
func (c *Client) Done() <-chan struct{} { return c.closed }

// Logout sends LOGOUT and closes.
func (c *Client) Logout(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = c.Execute(ctx, "LOGOUT", nil)
	c.Close()
}

// Caps returns the server capabilities, asking for them when unknown.
func (c *Client) Caps(ctx context.Context) (map[string]bool, error) {
	if c.caps == nil {
		if _, err := c.Execute(ctx, "CAPABILITY", nil); err != nil {
			return nil, err
		}
	}
	return c.caps, nil
}

// Has reports a capability (upper-case name, e.g. "QRESYNC", "AUTH=PLAIN").
// Call Caps first.
func (c *Client) Has(cap string) bool { return c.caps[cap] }

// Enabled reports whether ENABLE accepted the extension.
func (c *Client) Enabled(ext string) bool { return c.enabled[ext] }

func (c *Client) setCaps(vals []Value) {
	caps := map[string]bool{}
	for _, v := range vals {
		caps[strings.ToUpper(v.String())] = true
	}
	c.caps = caps
}

// Arg encoders for Execute.
type (
	// Atom is sent as is.
	Atom string
	// Literal is sent as a literal (LITERAL+ when available).
	Literal []byte
	// MailboxName is a mailbox name, encoded as modified UTF-7 and quoted.
	MailboxName string
)

// Execute sends one command and waits for its tagged answer. Untagged responses
// go to handle (may be nil) and, for mailbox events, to Options.Unsolicited.
// A NO, BAD or BYE answer is returned as *StatusError with the final response.
func (c *Client) Execute(ctx context.Context, name string, handle func(*Response), args ...any) (*Response, error) {
	return c.execute(ctx, name, handle, nil, args...)
}

// ExecuteSink is Execute with literals of the answer streamed to sink.
func (c *Client) ExecuteSink(ctx context.Context, name string, handle func(*Response), sink LiteralSink, args ...any) (*Response, error) {
	return c.execute(ctx, name, handle, sink, args...)
}

func (c *Client) execute(ctx context.Context, name string, handle func(*Response), sink LiteralSink, args ...any) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return nil, ErrClosed
	default:
	}
	// A cancelled command cannot be abandoned mid-stream: cancelling closes the
	// connection (deadline tricks lose against the per-response deadline below).
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	defer c.conn.SetDeadline(time.Time{})

	c.tagN++
	tag := "A" + strconv.Itoa(c.tagN)
	if c.opts.OnCommand != nil {
		c.opts.OnCommand(name)
	}
	if err := c.writeCommand(tag, name, args); err != nil {
		return nil, c.fail(ctx, err)
	}
	c.r.sink = sink
	defer func() { c.r.sink = nil }()
	for {
		resp, err := c.r.readResponse()
		if err != nil {
			return nil, c.fail(ctx, err)
		}
		c.conn.SetDeadline(time.Now().Add(c.opts.Timeout)) // long FETCH: time out on silence, not on size
		switch resp.Tag {
		case "+":
			if name == "AUTHENTICATE" {
				// SASL-IR answered with an error challenge (XOAUTH2): reply empty
				// to get the tagged NO instead of waiting for a timeout.
				c.bw.WriteString("\r\n")
				if err := c.bw.Flush(); err != nil {
					return nil, c.fail(ctx, err)
				}
			}
			continue
		case "*":
			c.untagged(resp, handle)
			if resp.Type == "BYE" && name != "LOGOUT" {
				c.Close()
				return resp, &StatusError{Command: name, Status: "BYE", Code: resp.Code, Text: resp.Text}
			}
			continue
		}
		if resp.Tag != tag {
			continue
		}
		if resp.Code == "CAPABILITY" {
			c.setCaps(resp.CodeArgs)
		}
		if resp.Type != "OK" {
			return resp, &StatusError{Command: name, Status: resp.Type, Code: resp.Code, Text: resp.Text}
		}
		return resp, nil
	}
}

// fail closes the connection after an I/O error; a cancelled ctx wins.
func (c *Client) fail(ctx context.Context, err error) error {
	c.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) untagged(resp *Response, handle func(*Response)) {
	switch resp.Type {
	case "CAPABILITY":
		c.setCaps(resp.Fields)
	case "ENABLED":
		for _, v := range resp.Fields {
			c.enabled[strings.ToUpper(v.String())] = true
		}
	case "OK":
		if resp.Code == "CAPABILITY" {
			c.setCaps(resp.CodeArgs)
		}
	}
	if c.selected != nil {
		c.selected.track(resp)
	}
	if handle != nil {
		handle(resp)
	}
	if c.opts.Unsolicited != nil && isEvent(resp) {
		c.opts.Unsolicited(resp)
	}
}

func isEvent(r *Response) bool {
	switch r.Type {
	case "EXISTS", "EXPUNGE", "FETCH", "VANISHED", "STATUS", "LIST", "RECENT":
		return true
	}
	return false
}

func (c *Client) writeCommand(tag, name string, args []any) error {
	c.bw.WriteString(tag)
	c.bw.WriteByte(' ')
	c.bw.WriteString(name)
	for _, a := range args {
		c.bw.WriteByte(' ')
		if err := c.writeArg(a); err != nil {
			return err
		}
	}
	c.bw.WriteString("\r\n")
	return c.bw.Flush()
}

func (c *Client) writeArg(a any) error {
	switch v := a.(type) {
	case Atom:
		c.bw.WriteString(string(v))
	case string:
		return c.writeString(v)
	case MailboxName:
		return c.writeString(EncodeMailbox(string(v)))
	case Literal:
		return c.writeLiteral(v)
	case []any:
		c.bw.WriteByte('(')
		for i, x := range v {
			if i > 0 {
				c.bw.WriteByte(' ')
			}
			if err := c.writeArg(x); err != nil {
				return err
			}
		}
		c.bw.WriteByte(')')
	case int:
		c.bw.WriteString(strconv.Itoa(v))
	case uint32:
		c.bw.WriteString(strconv.FormatUint(uint64(v), 10))
	case uint64:
		c.bw.WriteString(strconv.FormatUint(v, 10))
	default:
		return fmt.Errorf("imapc: unsupported argument %T", a)
	}
	return nil
}

// writeString sends s quoted, or as a literal when it holds CR, LF or 8-bit bytes.
func (c *Client) writeString(s string) error {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b == '\r' || b == '\n' || b >= 0x80 || b == 0 {
			return c.writeLiteral([]byte(s))
		}
	}
	c.bw.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			c.bw.WriteByte('\\')
		}
		c.bw.WriteByte(s[i])
	}
	c.bw.WriteByte('"')
	return nil
}

func (c *Client) writeLiteral(b []byte) error {
	if c.caps["LITERAL+"] || c.caps["LITERAL-"] && len(b) <= 4096 {
		fmt.Fprintf(c.bw, "{%d+}\r\n", len(b))
		c.bw.Write(b)
		return nil
	}
	fmt.Fprintf(c.bw, "{%d}\r\n", len(b))
	if err := c.bw.Flush(); err != nil {
		return err
	}
	for {
		resp, err := c.r.readResponse()
		if err != nil {
			return err
		}
		if resp.Tag == "+" {
			break
		}
		if resp.Tag != "*" {
			return &StatusError{Command: "literal", Status: resp.Type, Code: resp.Code, Text: resp.Text}
		}
		c.untagged(resp, nil)
	}
	c.bw.Write(b)
	return nil
}

// Login signs in with LOGIN, or AUTHENTICATE PLAIN when LOGINDISABLED.
func (c *Client) Login(ctx context.Context, user, password string) error {
	if _, err := c.Caps(ctx); err != nil {
		return err
	}
	if c.caps["LOGINDISABLED"] {
		return c.authPlain(ctx, user, password)
	}
	_, err := c.Execute(ctx, "LOGIN", nil, user, password)
	return c.afterAuth(ctx, err)
}

func (c *Client) authPlain(ctx context.Context, user, password string) error {
	ir := base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + password))
	return c.authenticate(ctx, "PLAIN", ir)
}

// LoginXOAuth2 signs in with AUTHENTICATE XOAUTH2 (Gmail, Microsoft).
func (c *Client) LoginXOAuth2(ctx context.Context, user, accessToken string) error {
	if _, err := c.Caps(ctx); err != nil {
		return err
	}
	ir := base64.StdEncoding.EncodeToString([]byte("user=" + user + "\x01auth=Bearer " + accessToken + "\x01\x01"))
	return c.authenticate(ctx, "XOAUTH2", ir)
}

func (c *Client) authenticate(ctx context.Context, mech, ir string) error {
	var err error
	if c.caps["SASL-IR"] {
		_, err = c.Execute(ctx, "AUTHENTICATE", nil, Atom(mech), Atom(ir))
	} else {
		err = c.authenticateContinued(ctx, mech, ir)
	}
	return c.afterAuth(ctx, err)
}

// authenticateContinued sends the initial response after the server's "+".
// An error challenge (XOAUTH2 failure details) is answered with an empty line.
func (c *Client) authenticateContinued(ctx context.Context, mech, ir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { c.conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	defer c.conn.SetDeadline(time.Time{})
	c.tagN++
	tag := "A" + strconv.Itoa(c.tagN)
	fmt.Fprintf(c.bw, "%s AUTHENTICATE %s\r\n", tag, mech)
	if err := c.bw.Flush(); err != nil {
		return c.fail(ctx, err)
	}
	sent := false
	for {
		resp, err := c.r.readResponse()
		if err != nil {
			return c.fail(ctx, err)
		}
		switch resp.Tag {
		case "+":
			line := ir
			if sent {
				line = "" // reply to an error challenge
			}
			sent = true
			c.bw.WriteString(line + "\r\n")
			if err := c.bw.Flush(); err != nil {
				return c.fail(ctx, err)
			}
		case "*":
			c.untagged(resp, nil)
		case tag:
			if resp.Code == "CAPABILITY" {
				c.setCaps(resp.CodeArgs)
			}
			if resp.Type != "OK" {
				return &StatusError{Command: "AUTHENTICATE", Status: resp.Type, Code: resp.Code, Text: resp.Text}
			}
			return nil
		}
	}
}

// afterAuth refreshes capabilities, which usually change after sign-in.
func (c *Client) afterAuth(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	c.caps = nil
	_, err = c.Caps(ctx)
	return err
}

// Enable sends ENABLE for the extensions the server advertises.
func (c *Client) Enable(ctx context.Context, exts ...string) error {
	var args []any
	for _, e := range exts {
		if c.caps[e] {
			args = append(args, Atom(e))
		}
	}
	if len(args) == 0 || !c.caps["ENABLE"] {
		return nil
	}
	_, err := c.Execute(ctx, "ENABLE", nil, args...)
	return err
}

// Noop sends NOOP, delivering pending events to Unsolicited.
func (c *Client) Noop(ctx context.Context) error {
	_, err := c.Execute(ctx, "NOOP", nil)
	return err
}

// ID sends the client's ID when the server supports it (some providers require it).
func (c *Client) ID(ctx context.Context, name, version string) {
	if c.caps["ID"] {
		_, _ = c.Execute(ctx, "ID", nil, []any{"name", name, "version", version})
	}
}

var _ io.Closer = (*Client)(nil)
