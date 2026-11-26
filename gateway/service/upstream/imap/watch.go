package imap

import (
	"context"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// idleRenew re-issues IDLE before the 30-minute server timeout (RFC 2177 asks
// clients to stay below 29 minutes).
const idleRenew = 25 * time.Minute

// Watch holds the listening connection: NOTIFY for every personal mailbox when
// the server has it, else IDLE on INBOX. Other folders are polled by the Runner.
func (u *Upstream) Watch(ctx context.Context, hint func(upstream.Hint)) error {
	var selected string
	onEvent := func(r *imapc.Response) {
		u.log.Debug("watch event", "type", r.Type, "num", r.Num)
		switch r.Type {
		case "STATUS":
			if name, _ := imapc.ParseStatus(r); name != "" {
				hint(upstream.Hint{Mailbox: name})
			}
		case "LIST":
			hint(upstream.Hint{}) // a folder was created, renamed or deleted
		case "EXISTS", "EXPUNGE", "FETCH", "VANISHED":
			hint(upstream.Hint{Mailbox: selected})
		}
	}
	c, err := u.dialWith(ctx, onEvent)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()

	caps := u.serverCaps()
	switch {
	case caps.notify:
		if err := c.NotifyPersonal(ctx); err != nil {
			if !isOperational(err) {
				return upstream.Classify("notify", err)
			}
			// NOTIFY refused (e.g. too many mailboxes): fall back to IDLE.
		} else {
			for {
				if err := c.Wait(ctx, idleRenew, nil); err != nil {
					return watchErr(ctx, err)
				}
			}
		}
		fallthrough
	case caps.idle:
		selected = u.watchFolder()
		if _, err := c.Select(ctx, selected, imapc.SelectOptions{ReadOnly: true}); err != nil {
			return upstream.Classify("select", err)
		}
		for {
			if err := c.Idle(ctx, idleRenew, nil); err != nil {
				return watchErr(ctx, err)
			}
		}
	}
	// No push at all: polling covers INBOX too. Keep the connection alive anyway,
	// so a dead server still shows up as unreachable.
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Minute):
			if err := c.Noop(ctx); err != nil {
				return watchErr(ctx, err)
			}
		}
	}
}

func watchErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return upstream.Classify("watch", err)
}
