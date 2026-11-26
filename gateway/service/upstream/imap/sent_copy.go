package imap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// sentPollInterval is how often Sent is searched for the server's copy.
const sentPollInterval = 700 * time.Millisecond

// pendingSent is a sent draft whose server copy may still show up in Sent. While
// waiting, a sync round that imports the copy binds it to the draft's id; after a
// timeout (late), a copy that arrives replaces the gateway's own one.
type pendingSent struct {
	emailID   string
	sentBox   string
	late      bool
	ourUID    uint32 // late: the gateway's copy in Sent
	boundUID  uint32 // waiting: bound by a sync round
	expires   time.Time
	learned   func(bool)
	expunging bool
	filing    bool // the gateway is filing its own copy (under the op lock)
}

// sentCopies are the pending sent drafts of an upstream, by Message-ID.
type sentCopies struct {
	mu   sync.Mutex
	byID map[string]*pendingSent
}

func (s *sentCopies) add(msgID string, p *pendingSent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]*pendingSent{}
	}
	now := time.Now()
	for k, v := range s.byID {
		if now.After(v.expires) {
			delete(s.byID, k)
		}
	}
	s.byID[msgID] = p
}

func (s *sentCopies) get(msgID string) *pendingSent {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.byID[msgID]
	if p != nil && time.Now().After(p.expires) {
		delete(s.byID, msgID)
		return nil
	}
	return p
}

func (s *sentCopies) remove(msgID string, p *pendingSent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID[msgID] == p {
		delete(s.byID, msgID)
	}
}

func (s *sentCopies) bound(p *pendingSent) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.boundUID
}

// PrepareSent announces a draft about to be sent, so a sync round that imports
// the server's copy before FileSent runs binds it to the draft instead of
// creating a new email. Call FileSent (or the returned cancel) afterwards.
func (u *Upstream) PrepareSent(draftID, sentMailboxID, messageID string) (cancel func()) {
	if messageID == "" {
		return func() {}
	}
	p := &pendingSent{emailID: draftID, sentBox: sentMailboxID, expires: time.Now().Add(sendTimeout + 2*upstream.SentCopyWait)}
	u.sent.add(messageID, p)
	return func() { u.sent.remove(messageID, p) }
}

// bindSentCopy is called by a sync round for every new message: a pending sent
// draft's server copy takes over the draft's id instead of becoming a new email.
// An entry whose email is gone or moved elsewhere meanwhile is dropped, and the
// message is imported normally.
func (u *Upstream) bindSentCopy(tx *mailindex.Tx, mailboxID string, m *imapc.Message, e *model.IndexEmail) (bool, error) {
	if e.MessageID == "" {
		return false, nil
	}
	p := u.sent.get(e.MessageID)
	if p == nil || p.sentBox != mailboxID {
		return false, nil
	}
	u.sent.mu.Lock()
	defer u.sent.mu.Unlock()
	drop := func() (bool, error) {
		if u.sent.byID[e.MessageID] == p {
			delete(u.sent.byID, e.MessageID)
		}
		return false, nil
	}
	switch {
	case p.late && m.UID != p.ourUID && !p.expunging:
		// Only while the gateway's copy is still where it filed it.
		var own model.IndexMembership
		if err := tx.DB().Where("mailbox_id = ? AND uid = ?", mailboxID, p.ourUID).First(&own).Error; err != nil || own.EmailID != p.emailID {
			return drop()
		}
		if err := tx.Bind(p.emailID, e, mailboxID, m.UID, m.ModSeq); err != nil {
			return false, err
		}
		if err := tx.RemoveUIDs(mailboxID, []uint32{p.ourUID}); err != nil {
			return false, err
		}
		p.expunging = true
		go u.dropOwnCopy(e.MessageID, p)
		return true, nil
	case !p.late && !p.filing && p.boundUID == 0:
		var n int64
		if err := tx.DB().Model(&model.IndexEmail{}).Where("id = ? AND orphaned_from = ''", p.emailID).Count(&n).Error; err != nil || n == 0 {
			return drop()
		}
		if err := tx.Bind(p.emailID, e, mailboxID, m.UID, m.ModSeq); err != nil {
			return false, err
		}
		p.boundUID = m.UID
		return true, nil
	}
	return false, nil
}

// dropOwnCopy deletes the gateway's own Sent copy after a late server copy took
// over its id. It runs after the sync round that bound the copy (op lock).
func (u *Upstream) dropOwnCopy(msgID string, p *pendingSent) {
	defer u.sent.remove(msgID, p)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	u.op.Lock()
	defer u.op.Unlock()
	if has, err := u.hasUID(p.sentBox, p.ourUID); err != nil || has {
		return // the round that bound the copy did not commit
	}
	m, err := u.mailbox(p.sentBox)
	if err != nil {
		return
	}
	err = u.withConn(ctx, func(c *imapc.Client) error { return expungeUIDs(ctx, c, m, []uint32{p.ourUID}) })
	if err != nil {
		u.log.Warn("delete the gateway's sent copy", "err", err)
		return
	}
	u.log.Info("server's late sent copy replaced the gateway's", "email", p.emailID)
	if p.learned != nil {
		p.learned(true)
	}
}

func (u *Upstream) hasUID(mailboxID string, uid uint32) (bool, error) {
	var n int64
	err := u.idx.DB().Model(&mailindex.Membership{}).Where("mailbox_id = ? AND uid = ?", mailboxID, uid).Count(&n).Error
	return n > 0, err
}

func expungeUIDs(ctx context.Context, c *imapc.Client, mailbox *model.IndexMailbox, uids []uint32) error {
	if err := selectRW(ctx, c, mailbox); err != nil {
		return err
	}
	if err := c.Store(ctx, uids, imapc.StoreAdd, []string{`\Deleted`}, nil); err != nil {
		return err
	}
	return c.UIDExpunge(ctx, uids)
}

// FileSent files a sent draft in Sent under its Email id: the gateway's own
// copy (the draft moved, or req.OwnCopy appended), or the server's own copy (see
// upstream.SentCopyServer).
func (u *Upstream) FileSent(ctx context.Context, req upstream.FileSentRequest) (*upstream.FileSentResult, error) {
	res := &upstream.FileSentResult{States: map[string]string{}}
	keywords := func() error {
		if len(req.AddKeywords) == 0 && len(req.RemoveKeywords) == 0 {
			return nil
		}
		st, err := u.SetKeywords(ctx, []string{req.DraftID}, req.AddKeywords, req.RemoveKeywords)
		maps.Copy(res.States, st)
		return err
	}
	sent, err := u.mailbox(req.SentMailboxID)
	if err != nil {
		return res, err
	}
	if req.Mode == upstream.SentCopyGateway || req.MessageID == "" {
		st, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
			return u.fileOwnLocked(ctx, c, states, req, sent)
		})
		maps.Copy(res.States, st)
		if err != nil {
			return res, err
		}
		return res, keywords()
	}

	p := u.sent.get(req.MessageID)
	if p == nil || p.emailID != req.DraftID {
		p = &pendingSent{emailID: req.DraftID, sentBox: req.SentMailboxID}
		u.sent.add(req.MessageID, p)
	}
	u.sent.mu.Lock()
	p.expires = time.Now().Add(sendTimeout + 2*upstream.SentCopyWait)
	u.sent.mu.Unlock()

	wait := req.Wait
	if wait <= 0 {
		wait = upstream.SentCopyWait
	}
	deadline := time.Now().Add(wait)
	for {
		uid := u.sent.bound(p)
		if uid == 0 {
			uids, err := u.searchMessageID(ctx, sent.Name, req.MessageID)
			if err != nil {
				u.sent.remove(req.MessageID, p)
				return res, err
			}
			uid = u.newestCandidate(sent.ID, req.DraftID, uids)
		}
		if uid != 0 {
			st, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
				return u.takeOverLocked(ctx, c, states, req.DraftID, sent, uid)
			})
			u.sent.remove(req.MessageID, p)
			maps.Copy(res.States, st)
			if err != nil {
				return res, err
			}
			res.ServerCopy = true
			if req.Mode == upstream.SentCopyAuto && req.Learned != nil {
				req.Learned(true)
			}
			return res, keywords()
		}
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		select {
		case <-ctx.Done():
			u.sent.remove(req.MessageID, p)
			return res, ctx.Err()
		case <-time.After(min(sentPollInterval, left)):
		}
	}

	// No server copy in time: file the gateway's own, unless a sync round bound
	// the server's meanwhile (checked under the op lock, so no round runs between
	// the check and the filing), and let a late copy replace it.
	serverCopy := false
	st, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		if uid := u.sent.bound(p); uid != 0 {
			serverCopy = true
			return u.takeOverLocked(ctx, c, states, req.DraftID, sent, uid)
		}
		u.sent.mu.Lock()
		p.filing = true
		u.sent.mu.Unlock()
		if err := u.fileOwnLocked(ctx, c, states, req, sent); err != nil {
			return err
		}
		var own model.IndexMembership
		if err := u.idx.DB().Where("email_id = ? AND mailbox_id = ?", req.DraftID, sent.ID).First(&own).Error; err != nil {
			u.sent.remove(req.MessageID, p)
			return nil
		}
		u.sent.mu.Lock()
		p.late, p.filing, p.ourUID, p.expires, p.learned = true, false, own.UID, time.Now().Add(upstream.LateCopyWindow), req.Learned
		u.sent.mu.Unlock()
		return nil
	})
	maps.Copy(res.States, st)
	if err != nil || serverCopy {
		u.sent.remove(req.MessageID, p)
	}
	if err != nil {
		return res, err
	}
	res.ServerCopy = serverCopy
	if req.Mode == upstream.SentCopyAuto && req.Learned != nil {
		req.Learned(serverCopy)
	}
	return res, keywords()
}

// fileOwnLocked files the gateway's own copy: req.OwnCopy appended to Sent and
// bound to the draft's id (the delivered message differs from the draft), or the
// draft itself moved there.
func (u *Upstream) fileOwnLocked(ctx context.Context, c *imapc.Client, states map[string]string, req upstream.FileSentRequest, sent *model.IndexMailbox) error {
	if len(req.OwnCopy) > 0 {
		au, err := c.Append(ctx, sent.Name, []string{`\Seen`}, time.Time{}, req.OwnCopy)
		if err != nil {
			return err
		}
		if au != nil && (sent.UIDValidity == 0 || au.UIDValidity == sent.UIDValidity) {
			return u.takeOverLocked(ctx, c, states, req.DraftID, sent, au.UID)
		}
		// No APPENDUID: the appended copy is found by a later sync as a new
		// email; move the draft instead so the id stays.
		u.log.Warn("server gave no APPENDUID; filing the draft itself", "email", req.DraftID)
	}
	return u.moveLocked(ctx, c, states, []string{req.DraftID}, sent.ID)
}

// newestCandidate picks the newest UID that may be the draft's server copy:
// not yet in the index, or already bound to the draft. A message bound to
// another email is an older one with the same Message-ID.
func (u *Upstream) newestCandidate(sentID, draftID string, uids []uint32) uint32 {
	slices.Sort(uids)
	for i := len(uids) - 1; i >= 0; i-- {
		var m model.IndexMembership
		err := u.idx.DB().Where("mailbox_id = ? AND uid = ?", sentID, uids[i]).First(&m).Error
		if err != nil || m.EmailID == draftID {
			return uids[i]
		}
	}
	return 0
}

// searchMessageID returns the UIDs of a folder's messages with a Message-ID.
func (u *Upstream) searchMessageID(ctx context.Context, mailbox, msgID string) ([]uint32, error) {
	var uids []uint32
	err := u.withConn(ctx, func(c *imapc.Client) error {
		// A fresh SELECT, so messages delivered since the last one are visible.
		if _, err := c.Select(ctx, mailbox, imapc.SelectOptions{ReadOnly: true}); err != nil {
			return err
		}
		var err error
		uids, err = c.SearchUIDs(ctx, imapc.Atom("HEADER"), imapc.Atom("Message-ID"), "<"+msgID+">")
		return err
	})
	return uids, err
}

// takeOverLocked binds the server's Sent copy (uid) to the draft and deletes the
// draft, in one index commit. The op lock is held.
func (u *Upstream) takeOverLocked(ctx context.Context, c *imapc.Client, states map[string]string, draftID string, sent *model.IndexMailbox, uid uint32) error {
	var current model.IndexMembership
	bound := u.idx.DB().Where("mailbox_id = ? AND uid = ?", sent.ID, uid).First(&current).Error == nil
	if bound && current.EmailID != draftID {
		return rejected("sent copy", fmt.Errorf("%w: uid %d belongs to another email", ErrNotFound, uid))
	}
	var meta *fetched
	if !bound {
		if _, err := c.Select(ctx, sent.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
			return err
		}
		msgs, err := u.fetchMeta(ctx, c, []uint32{uid})
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return rejected("sent copy", fmt.Errorf("%w: sent copy vanished", ErrNotFound))
		}
		meta = msgs[0]
	}
	where, err := u.locate([]string{draftID})
	if err != nil {
		return err
	}
	drafts := map[string][]uint32{}
	for mboxID, list := range where {
		if mboxID == sent.ID {
			continue
		}
		m, err := u.mailbox(mboxID)
		if err != nil {
			return err
		}
		uids := uidsOf(list)
		if err := expungeUIDs(ctx, c, m, uids); err != nil {
			return err
		}
		drafts[mboxID] = uids
	}
	if err := u.commit(states, func(tx *mailindex.Tx) error {
		if meta != nil {
			e, _ := toEmail(meta.msg, u.stableKey(meta.msg), u.threadKey(meta.msg))
			e.Preview = meta.preview
			if err := tx.Bind(draftID, e, sent.ID, uid, meta.msg.ModSeq); err != nil {
				return err
			}
		}
		for mboxID, uids := range drafts {
			if err := tx.RemoveUIDs(mboxID, uids); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !u.serverCaps().uidplus {
		for mboxID := range drafts {
			if m, err := u.mailbox(mboxID); err == nil {
				if err := u.resync(ctx, c, states, m.Name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// TestSentCopy sends a message to the upstream's own address and reports whether
// the server stored a copy in Sent within wait. The test message is deleted from
// Sent and the inbox afterwards.
func (u *Upstream) TestSentCopy(ctx context.Context, wait time.Duration) (bool, error) {
	if wait <= 0 {
		wait = upstream.SentCopyWait
	}
	sent, err := u.roleMailbox("sent")
	if err != nil {
		return false, err
	}
	inbox, _ := u.roleMailbox("inbox")
	addr := u.cfg.Email
	domain := "gateway.invalid"
	if _, d, ok := strings.Cut(addr, "@"); ok && d != "" {
		domain = d
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	msgID := "sent-copy-test." + hex.EncodeToString(rnd[:]) + "@" + domain
	raw := "From: <" + addr + ">\r\nTo: <" + addr + ">\r\n" +
		"Subject: czlmail gateway: sent mail check\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: <" + msgID + ">\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nAuto-Submitted: auto-generated\r\n\r\n" +
		"The czlmail gateway checks whether this mail server keeps a copy of sent mail.\r\n" +
		"This message is deleted automatically.\r\n"
	if _, err := u.Send(ctx, &upstream.Outgoing{MailFrom: addr, RcptTo: []string{addr}, Raw: []byte(raw), MessageID: msgID}); err != nil {
		return false, err
	}
	saved := false
	deadline := time.Now().Add(wait)
	for !saved {
		uids, err := u.searchMessageID(ctx, sent.Name, msgID)
		if err != nil {
			return false, err
		}
		saved = len(uids) > 0
		if saved || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(sentPollInterval):
		}
	}
	boxes := []*model.IndexMailbox{sent}
	if inbox != nil {
		boxes = append(boxes, inbox)
	}
	for _, m := range boxes {
		uids, err := u.searchMessageID(ctx, m.Name, msgID)
		if err != nil || len(uids) == 0 {
			continue
		}
		if err := u.withConn(ctx, func(c *imapc.Client) error { return expungeUIDs(ctx, c, m, uids) }); err != nil {
			u.log.Warn("delete sent-copy test message", "mailbox", m.Name, "err", err)
		}
	}
	return saved, nil
}

func (u *Upstream) roleMailbox(role string) (*model.IndexMailbox, error) {
	var m model.IndexMailbox
	if err := u.idx.DB().Where("role = ?", role).Order("name").First(&m).Error; err != nil {
		return nil, rejected("mailbox", errors.Join(ErrNotFound, fmt.Errorf("no %s mailbox", role)))
	}
	return &m, nil
}
