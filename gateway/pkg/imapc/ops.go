package imapc

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Store modes.
const (
	StoreAdd     = "+FLAGS.SILENT"
	StoreRemove  = "-FLAGS.SILENT"
	StoreReplace = "FLAGS.SILENT"
)

// Store changes flags of UIDs. With CONDSTORE enabled the server answers with
// FETCH (UID MODSEQ ...) for each changed message, which go to fn (may be nil).
func (c *Client) Store(ctx context.Context, uids UIDSet, mode string, flags []string, fn func(*Message)) error {
	list := make([]any, len(flags))
	for i, f := range flags {
		list[i] = Atom(f)
	}
	_, err := c.Execute(ctx, "UID STORE", func(r *Response) {
		if r.Type == "FETCH" && fn != nil {
			if m, err := parseFetch(r); err == nil {
				fn(m)
			}
		}
	}, Atom(uids.Compact()), Atom(mode), list)
	return err
}

// SearchUIDs runs UID SEARCH with raw criteria (e.g. "ALL", "HEADER Message-ID <x>").
func (c *Client) SearchUIDs(ctx context.Context, criteria ...any) (UIDSet, error) {
	var out UIDSet
	_, err := c.Execute(ctx, "UID SEARCH", func(r *Response) {
		switch r.Type {
		case "SEARCH":
			for _, v := range r.Fields {
				if v.Kind == KindNumber {
					out = append(out, uint32(v.Num))
				}
			}
		case "ESEARCH":
			for i := 0; i+1 < len(r.Fields); i++ {
				if r.Fields[i].IsAtom("ALL") {
					set, err := ParseUIDSet(r.Fields[i+1].String())
					if err == nil {
						out = append(out, set...)
					}
				}
			}
		}
	}, criteria...)
	return out, err
}

// CopyUID is the COPYUID response code: source UIDs map to destination UIDs in order.
type CopyUID struct {
	UIDValidity uint32
	Source      UIDSet
	Dest        UIDSet
}

// Map returns source UID → destination UID.
func (c *CopyUID) Map() map[uint32]uint32 {
	out := map[uint32]uint32{}
	if c == nil || len(c.Source) != len(c.Dest) {
		return out
	}
	for i, s := range c.Source {
		out[s] = c.Dest[i]
	}
	return out
}

func parseCopyUID(code string, args []Value) *CopyUID {
	if code != "COPYUID" || len(args) < 3 {
		return nil
	}
	src, err1 := ParseUIDSet(args[1].String())
	dst, err2 := ParseUIDSet(args[2].String())
	if err1 != nil || err2 != nil {
		return nil
	}
	return &CopyUID{UIDValidity: uint32(args[0].Num), Source: src, Dest: dst}
}

// Move moves UIDs with UID MOVE. The COPYUID code arrives in an untagged OK (RFC 6851).
func (c *Client) Move(ctx context.Context, uids UIDSet, dest string) (*CopyUID, error) {
	var cu *CopyUID
	resp, err := c.Execute(ctx, "UID MOVE", func(r *Response) {
		if r.Type == "OK" && r.Code == "COPYUID" {
			cu = parseCopyUID(r.Code, r.CodeArgs)
		}
	}, Atom(uids.Compact()), MailboxName(dest))
	if err != nil {
		return nil, err
	}
	if cu == nil {
		cu = parseCopyUID(resp.Code, resp.CodeArgs)
	}
	return cu, nil
}

// Copy copies UIDs.
func (c *Client) Copy(ctx context.Context, uids UIDSet, dest string) (*CopyUID, error) {
	resp, err := c.Execute(ctx, "UID COPY", nil, Atom(uids.Compact()), MailboxName(dest))
	if err != nil {
		return nil, err
	}
	return parseCopyUID(resp.Code, resp.CodeArgs), nil
}

// AppendUID is the APPENDUID response code.
type AppendUID struct {
	UIDValidity uint32
	UID         uint32
}

// Append stores a message. date may be zero (the server uses now).
func (c *Client) Append(ctx context.Context, mailbox string, flags []string, date time.Time, msg []byte) (*AppendUID, error) {
	args := []any{MailboxName(mailbox)}
	fl := make([]any, len(flags))
	for i, f := range flags {
		fl[i] = Atom(f)
	}
	args = append(args, fl)
	if !date.IsZero() {
		args = append(args, date.Format(`02-Jan-2006 15:04:05 -0700`))
	}
	args = append(args, Literal(msg))
	resp, err := c.Execute(ctx, "APPEND", nil, args...)
	if err != nil {
		return nil, err
	}
	if resp.Code == "APPENDUID" && len(resp.CodeArgs) >= 2 {
		return &AppendUID{UIDValidity: uint32(resp.CodeArgs[0].Num), UID: uint32(resp.CodeArgs[1].Num)}, nil
	}
	return nil, nil
}

// UIDExpunge permanently removes UIDs marked \Deleted (UIDPLUS); without UIDPLUS
// it falls back to EXPUNGE, which also removes other \Deleted messages.
func (c *Client) UIDExpunge(ctx context.Context, uids UIDSet) error {
	if c.caps["UIDPLUS"] {
		_, err := c.Execute(ctx, "UID EXPUNGE", nil, Atom(uids.Compact()))
		return err
	}
	_, err := c.Execute(ctx, "EXPUNGE", nil)
	return err
}

// ErrIdleUnsupported is returned by Idle when the server lacks IDLE.
var ErrIdleUnsupported = errors.New("imapc: server has no IDLE")

// Idle runs IDLE until ctx ends, maxWait passes, or wake returns true for an
// untagged response, then sends DONE. Responses also reach Options.Unsolicited.
// It returns nil after a normal DONE.
func (c *Client) Idle(ctx context.Context, maxWait time.Duration, wake func(*Response) bool) error {
	if !c.caps["IDLE"] {
		return ErrIdleUnsupported
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagN++
	tag := "A" + strconv.Itoa(c.tagN)
	c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	c.bw.WriteString(tag + " IDLE\r\n")
	if err := c.bw.Flush(); err != nil {
		return c.fail(ctx, err)
	}
	// Wait for "+", then read until woken; DONE is written from another goroutine
	// on timeout or cancellation, so the reader below sees the tagged OK.
	resp, err := c.r.readResponse()
	for err == nil && resp.Tag == "*" {
		c.untagged(resp, nil)
		resp, err = c.r.readResponse()
	}
	if err != nil {
		return c.fail(ctx, err)
	}
	if resp.Tag != "+" {
		return &StatusError{Command: "IDLE", Status: resp.Type, Code: resp.Code, Text: resp.Text}
	}
	c.conn.SetDeadline(time.Time{})

	done := make(chan struct{})
	var once bool
	sendDone := func() {
		if once {
			return
		}
		once = true
		close(done)
	}
	writerErr := make(chan error, 1)
	go func() {
		timer := time.NewTimer(maxWait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		case <-done:
		}
		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := c.conn.Write([]byte("DONE\r\n"))
		c.conn.SetReadDeadline(time.Now().Add(c.opts.Timeout))
		writerErr <- err
	}()
	for {
		resp, err := c.r.readResponse()
		if err != nil {
			sendDone()
			<-writerErr
			return c.fail(ctx, err)
		}
		if resp.Tag == "*" {
			c.untagged(resp, nil)
			if resp.Type == "BYE" {
				sendDone()
				<-writerErr
				c.Close()
				return &StatusError{Command: "IDLE", Status: "BYE", Code: resp.Code, Text: resp.Text}
			}
			if wake != nil && wake(resp) {
				sendDone()
			}
			continue
		}
		if resp.Tag == tag {
			sendDone()
			werr := <-writerErr
			c.conn.SetDeadline(time.Time{})
			if resp.Type != "OK" {
				return &StatusError{Command: "IDLE", Status: resp.Type, Code: resp.Code, Text: resp.Text}
			}
			if werr != nil {
				return c.fail(ctx, werr)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}
}

// NotifyPersonal sends NOTIFY SET STATUS for all personal mailboxes (RFC 5465):
// message arrivals, expunges and flag changes in any mailbox arrive as STATUS
// responses, mailbox creation / rename / deletion as LIST responses.
func (c *Client) NotifyPersonal(ctx context.Context) error {
	events := []any{Atom("MessageNew"), Atom("MessageExpunge"), Atom("FlagChange"), Atom("MailboxName"), Atom("SubscriptionChange")}
	_, err := c.Execute(ctx, "NOTIFY", nil, Atom("SET"), Atom("STATUS"), []any{Atom("personal"), events})
	return err
}

// Wait delivers unsolicited responses (after NOTIFY) to Options.Unsolicited until
// ctx ends or maxWait passes. It idles when the server has IDLE and otherwise
// sends NOOP every 30 seconds. wake ends the wait early; it returns nil then.
func (c *Client) Wait(ctx context.Context, maxWait time.Duration, wake func(*Response) bool) error {
	if c.caps["IDLE"] {
		return c.Idle(ctx, maxWait, wake)
	}
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		woken := false
		_, err := c.Execute(ctx, "NOOP", func(r *Response) {
			if wake != nil && wake(r) {
				woken = true
			}
		})
		if err != nil || woken {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(30*time.Second, time.Until(deadline))):
		}
	}
	return nil
}
