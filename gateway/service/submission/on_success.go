package submission

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// emailPlan is the onSuccessUpdateEmail patch or onSuccessDestroyEmail entry of
// one submission. A patch that moves the draft into the Sent mailbox files it
// there under its id (see upstream.SentFiler).
type emailPlan struct {
	s       *Service
	patch   map[string]json.RawMessage // nil for destroy
	destroy bool

	// Filled while sending.
	emailID    string
	messageID  string
	ownCopy    []byte // the delivered bytes to file when they differ from the draft
	cancelPrep func()
}

func (p *emailPlan) cancel() {
	if p.cancelPrep != nil {
		p.cancelPrep()
		p.cancelPrep = nil
	}
}

// emailChange is a patch resolved against the email's current state.
type emailChange struct {
	current     []string // mailboxes now
	target      string   // "" when the mailbox does not change
	targetRole  string
	add, remove []string
}

// resolveChange applies the patch to the email's mailboxes and keywords.
func (p *emailPlan) resolveChange(idx *mailindex.Store) (*emailChange, setError) {
	var row model.IndexEmail
	if err := idx.DB().Select("id", "keywords").Where("id = ? AND orphaned_from = ''", p.emailID).First(&row).Error; err != nil {
		return nil, newSetError("notFound", "")
	}
	var current []string
	if err := idx.DB().Model(&mailindex.Membership{}).Where("email_id = ?", p.emailID).Pluck("mailbox_id", &current).Error; err != nil {
		return nil, newSetError("serverFail", "")
	}
	boxes := map[string]bool{}
	for _, m := range current {
		boxes[m] = true
	}
	kw := mailindex.ParseKeywords(row.Keywords)
	before := maps.Clone(kw)
	for key, raw := range p.patch {
		val := strings.TrimSpace(string(raw))
		switch {
		case key == "mailboxIds":
			var m map[string]bool
			if json.Unmarshal(raw, &m) != nil {
				return nil, invalidProperties("", key)
			}
			boxes = map[string]bool{}
			for id, on := range m {
				if on {
					boxes[id] = true
				}
			}
		case strings.HasPrefix(key, "mailboxIds/"):
			id := strings.TrimPrefix(key, "mailboxIds/")
			switch val {
			case "true":
				boxes[id] = true
			case "null", "false":
				delete(boxes, id)
			default:
				return nil, invalidProperties("", key)
			}
		case key == "keywords":
			var m map[string]bool
			if json.Unmarshal(raw, &m) != nil {
				return nil, invalidProperties("", key)
			}
			kw = mailindex.Keywords{}
			for k, on := range m {
				if on {
					kw[strings.ToLower(k)] = true
				}
			}
		case strings.HasPrefix(key, "keywords/"):
			k := strings.ToLower(strings.TrimPrefix(key, "keywords/"))
			switch val {
			case "true":
				kw[k] = true
			case "null", "false":
				delete(kw, k)
			default:
				return nil, invalidProperties("", key)
			}
		default:
			return nil, invalidProperties("Only mailboxIds and keywords can change after sending.", key)
		}
	}
	ch := &emailChange{current: current}
	for k := range kw {
		if !before[k] {
			ch.add = append(ch.add, k)
		}
	}
	for k := range before {
		if !kw[k] {
			ch.remove = append(ch.remove, k)
		}
	}
	slices.Sort(ch.add)
	slices.Sort(ch.remove)
	switch {
	case len(boxes) == 0:
		// Removing the last mailbox (no Sent folder): leave it where it is.
	case len(boxes) > 1:
		return nil, invalidProperties("An email can be in one mailbox only.", "mailboxIds")
	default:
		for id := range boxes {
			if len(current) != 1 || current[0] != id {
				ch.target = id
			}
		}
	}
	if ch.target != "" {
		var mb model.IndexMailbox
		if err := idx.DB().Select("id", "role").Where("id = ?", ch.target).First(&mb).Error; err != nil {
			return nil, invalidProperties("Unknown mailbox.", "mailboxIds")
		}
		ch.targetRole = mb.Role
	}
	return ch, nil
}

// sentMode is how this submission's copy reaches Sent.
func (p *emailPlan) sentMode(t *sendTarget) string {
	switch t.row.SentCopy {
	case upstream.SentCopyGateway, upstream.SentCopyServer:
		return t.row.SentCopy
	}
	return upstream.SentCopyAuto
}

// prepare announces the draft to the upstream before sending when it will be
// filed in Sent by the server-copy strategy.
func (p *emailPlan) prepare(t *sendTarget) {
	if p.destroy || p.messageID == "" {
		return
	}
	filer, ok := t.handle.Upstream.(upstream.SentFiler)
	if !ok || p.sentMode(t) == upstream.SentCopyGateway {
		return
	}
	ch, serr := p.resolveChange(t.handle.Upstream.Index())
	if serr != nil || ch.target == "" || ch.targetRole != "sent" {
		return
	}
	p.cancelPrep = filer.PrepareSent(p.emailID, ch.target, p.messageID)
}

// apply runs the plan after a successful send and returns the new mail states.
func (p *emailPlan) apply(ctx context.Context, t *sendTarget) (map[string]string, setError) {
	defer p.cancel()
	// Filing may wait for the server's copy; a client that hangs up must not
	// leave the draft half filed.
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer stop()
	up := t.handle.Upstream
	if p.destroy {
		st, err := up.Destroy(ctx, []string{p.emailID})
		if err != nil {
			return st, upstreamSetError(err)
		}
		return st, nil
	}
	ch, serr := p.resolveChange(up.Index())
	if serr != nil {
		return nil, serr
	}
	states := map[string]string{}
	filer, canFile := up.(upstream.SentFiler)
	if ch.target != "" && ch.targetRole == "sent" && canFile {
		// FileSent takes over the announcement (and keeps it for a late copy).
		p.cancelPrep = nil
		mode := p.sentMode(t)
		res, err := filer.FileSent(ctx, upstream.FileSentRequest{
			DraftID: p.emailID, SentMailboxID: ch.target, MessageID: p.messageID, Mode: mode, OwnCopy: p.ownCopy,
			AddKeywords: ch.add, RemoveKeywords: ch.remove, Wait: p.s.opts.SentCopyWait,
			Learned: func(serverSaves bool) { p.s.learnSentCopy(t.accountID, mode, serverSaves) },
		})
		if res != nil {
			maps.Copy(states, res.States)
		}
		if err != nil {
			p.s.log.Warn("file sent message", "account", t.accountID, "email", p.emailID, "err", err)
			return states, upstreamSetError(err)
		}
		return states, nil
	}
	if ch.target != "" {
		st, err := up.Move(ctx, []string{p.emailID}, ch.target)
		maps.Copy(states, st)
		if err != nil {
			return states, upstreamSetError(err)
		}
	}
	if len(ch.add) > 0 || len(ch.remove) > 0 {
		st, err := up.SetKeywords(ctx, []string{p.emailID}, ch.add, ch.remove)
		maps.Copy(states, st)
		if err != nil {
			return states, upstreamSetError(err)
		}
	}
	return states, nil
}

// learnSentCopy stores what a send showed about the server: an undecided
// upstream takes the observed strategy; a server copy that turned up late
// switches the upstream to the server strategy.
func (s *Service) learnSentCopy(accountID, mode string, serverSaves bool) {
	q := s.db.Model(&model.Upstream{}).Where("public_id = ?", accountID)
	value := upstream.SentCopyGateway
	if serverSaves {
		value = upstream.SentCopyServer
	}
	if mode == upstream.SentCopyAuto && !serverSaves {
		q = q.Where("sent_copy = ?", upstream.SentCopyAuto)
	}
	if err := q.Update("sent_copy", value).Error; err != nil {
		s.log.Warn("save sent copy strategy", "account", accountID, "err", err)
		return
	}
	s.log.Info("sent copy strategy learned", "account", accountID, "sent_copy", value)
}
