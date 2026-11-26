package imap

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// SendMessage sends through users.messages.send and indexes the sent message
// at once; see upstream.MessageSender. The API's id is the message's X-GM-MSGID
// (in hex): the message is found in All Mail by it. With a draft, the draft is
// deleted and the sent message takes the draft's Email id.
//
// The whole send holds the op lock, so a sync round cannot index the sent
// message as a new email in between.
func (g *Gmail) SendMessage(ctx context.Context, raw []byte, opts upstream.SendOptions) (*upstream.SendResult, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return nil, err
	}
	var draft *gmEmail
	if opts.DraftEmailID != "" {
		list, err := g.loadEmails(mb, []string{opts.DraftEmailID})
		if err != nil {
			return nil, err
		}
		draft = list[0]
	}
	threadID := g.gmThread(opts, raw)
	res := &upstream.SendResult{}
	states, err := g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		sent, err := g.gm.api.send(ctx, raw, threadID)
		if err != nil {
			return err
		}
		n, err := strconv.ParseUint(sent.ID, 16, 64)
		if err != nil {
			return &upstream.Error{Kind: upstream.KindUnreachable, Op: "gmail send", Err: err}
		}
		msg := &gmEmail{id: "sent", msgID: strconv.FormatUint(n, 10)}
		uid, err := g.findInAllMail(ctx, c, mb, msg)
		if err != nil {
			return err
		}
		if draft != nil {
			if err := g.dropDraft(ctx, c, states, mb, draft); err != nil {
				return err
			}
		}
		if uid == 0 {
			return nil // not visible yet: the next sync imports it
		}
		if draft == nil {
			ids, err := g.importUIDs(ctx, c, states, mb.lr.allMail, []uint32{uid})
			if err != nil {
				return err
			}
			res.EmailID = ids[uid]
		} else {
			if err := g.bindTo(ctx, c, states, mb, uid, draft.id); err != nil {
				return err
			}
			res.EmailID = draft.id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.States = states
	if res.EmailID == "" && draft != nil {
		res.EmailID = draft.id
	}
	if res.EmailID != "" {
		var e model.IndexEmail
		if g.idx.DB().Select("thread_id").Where("id = ?", res.EmailID).First(&e).Error == nil {
			res.ThreadID = e.ThreadID
		}
	}
	return res, nil
}

// gmThread picks the Gmail threadId (hex X-GM-THRID) for a message: the
// draft's, the answered email's, or the thread of its In-Reply-To.
func (g *Gmail) gmThread(opts upstream.SendOptions, raw []byte) string {
	db := g.idx.DB()
	for _, id := range []string{opts.DraftEmailID, opts.InReplyToEmailID} {
		if id == "" {
			continue
		}
		var e model.IndexEmail
		if db.Select("upstream_thread_id").Where("id = ?", id).First(&e).Error == nil {
			if t := gmHex(e.UpstreamThreadID); t != "" {
				return t
			}
		}
	}
	if irt := firstID(headerValue(raw, "In-Reply-To")); irt != "" {
		var e model.IndexEmail
		if db.Select("upstream_thread_id").Where("message_id = ? AND orphaned_from = ''", irt).First(&e).Error == nil {
			return gmHex(e.UpstreamThreadID)
		}
	}
	return ""
}

// gmHex turns "gm:<decimal>" into the API's hexadecimal id.
func gmHex(key string) string {
	n, err := strconv.ParseUint(strings.TrimPrefix(key, "gm:"), 10, 64)
	if err != nil || !strings.HasPrefix(key, "gm:") {
		return ""
	}
	return strconv.FormatUint(n, 16)
}

// findInAllMail finds a message just sent, retrying briefly while Gmail files it.
func (g *Gmail) findInAllMail(ctx context.Context, c *imapc.Client, mb *gmMailboxes, e *gmEmail) (uint32, error) {
	for attempt := 0; ; attempt++ {
		// A fresh SELECT, so the folder view includes the new message.
		if err := c.Unselect(ctx); err != nil && !isOperational(err) {
			return 0, err
		}
		found, err := searchMsgIDs(ctx, c, mb.lr.allMail.Name, []*gmEmail{e})
		if err != nil {
			return 0, err
		}
		if uid := found[e.id]; uid != 0 || attempt >= 3 {
			return uid, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 300 * time.Millisecond):
		}
	}
}

// dropDraft deletes a sent draft on the server: to Trash, then expunged there.
// Its index row stays, for bindTo.
func (g *Gmail) dropDraft(ctx context.Context, c *imapc.Client, states map[string]string, mb *gmMailboxes, d *gmEmail) error {
	if d.src == mb.lr.allMail {
		if err := g.moveOut(ctx, c, states, []*gmEmail{d}, mb.trash, false); err != nil {
			return err
		}
	}
	if d.src == mb.lr.allMail {
		return errors.New("gmail: draft not found in Trash after moving it")
	}
	if err := selectRW(ctx, c, d.src); err != nil {
		return err
	}
	uids := imapc.UIDSet{d.uid}
	if err := c.Store(ctx, uids, imapc.StoreAdd, []string{`\Deleted`}, nil); err != nil {
		return err
	}
	return c.UIDExpunge(ctx, uids)
}

// bindTo indexes All Mail's uid as emailID (the sent draft).
func (g *Gmail) bindTo(ctx context.Context, c *imapc.Client, states map[string]string, mb *gmMailboxes, uid uint32, emailID string) error {
	all := mb.lr.allMail
	if _, err := c.Select(ctx, all.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
		return err
	}
	msgs, err := g.fetchMeta(ctx, c, []uint32{uid})
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	f := msgs[0]
	return g.commit(states, func(tx *mailindex.Tx) error {
		e, refs := toEmail(f.msg, g.stableKey(f.msg), g.threadKey(f.msg))
		e.Preview = f.preview
		if err := tx.ReplaceContent(emailID, e, refs, all.ID, uid, f.msg.ModSeq); err != nil {
			return err
		}
		if err := tx.SetTrashLabels(emailID, nil); err != nil {
			return err
		}
		lr, err := loadLabelRows(tx)
		if err != nil {
			return err
		}
		return g.placeLabels(tx, lr, all, emailID, f.msg)
	})
}
