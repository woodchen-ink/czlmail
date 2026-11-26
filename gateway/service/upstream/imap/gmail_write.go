package imap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// Gmail writes. Mailbox changes map to labels: adding a label mailbox is
// +X-GM-LABELS, removing one -X-GM-LABELS, Archive means "without \Inbox",
// Trash and Spam are real moves. Moving to Trash remembers the email's labels,
// so leaving Trash again restores them.

// gmEmail is an email with where it is.
type gmEmail struct {
	id      string
	msgID   string // X-GM-MSGID, decimal
	src     *model.IndexMailbox
	uid     uint32
	labels  []string // current visible labels (All Mail only)
	trashed []string // labels remembered when the gateway trashed it
}

type gmMailboxes struct {
	lr    *labelRows
	trash *model.IndexMailbox
	spam  *model.IndexMailbox
	byID  map[string]*model.IndexMailbox
}

func (g *Gmail) loadMailboxes() (*gmMailboxes, error) {
	var rows []model.IndexMailbox
	if err := g.idx.DB().Find(&rows).Error; err != nil {
		return nil, err
	}
	mb := &gmMailboxes{lr: &labelRows{byLabel: map[string]*model.IndexMailbox{}}, byID: map[string]*model.IndexMailbox{}}
	for i := range rows {
		m := &rows[i]
		mb.byID[m.ID] = m
		switch {
		case m.Hidden:
			mb.lr.allMail = m
		case m.Virtual && m.Label == "":
			mb.lr.archive = m
		case m.Virtual:
			mb.lr.byLabel[m.Label] = m
		case m.Role == "trash":
			mb.trash = m
		case m.Role == "junk":
			mb.spam = m
		}
	}
	if mb.lr.allMail == nil || mb.trash == nil {
		return nil, rejected("gmail", errors.New("All Mail or Trash not found; is IMAP enabled for this account?"))
	}
	return mb, nil
}

// visible returns a mailbox id a client may use, or a rejection.
func (mb *gmMailboxes) visible(id string) (*model.IndexMailbox, error) {
	m := mb.byID[id]
	if m == nil || m.Hidden {
		return nil, rejected("mailbox", fmt.Errorf("%w: mailbox %s", ErrNotFound, id))
	}
	return m, nil
}

func (g *Gmail) loadEmails(mb *gmMailboxes, ids []string) ([]*gmEmail, error) {
	var rows []model.IndexEmail
	if err := g.idx.DB().Select("id", "upstream_id", "trash_labels").Where("id IN ? AND orphaned_from = ''", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	var ms []model.IndexMembership
	if err := g.idx.DB().Where("email_id IN ?", ids).Find(&ms).Error; err != nil {
		return nil, err
	}
	byID := map[string]*gmEmail{}
	for _, r := range rows {
		byID[r.ID] = &gmEmail{id: r.ID, msgID: strings.TrimPrefix(r.UpstreamID, "gm:"), trashed: mailindex.DecodeStrings(r.TrashLabels)}
	}
	for _, m := range ms {
		e, box := byID[m.EmailID], mb.byID[m.MailboxID]
		if e == nil || box == nil {
			continue
		}
		switch {
		case box.Virtual && box.Label != "":
			e.labels = append(e.labels, box.Label)
		case box.Virtual:
		default:
			e.src, e.uid = box, m.UID
		}
	}
	out := make([]*gmEmail, 0, len(ids))
	for _, id := range ids {
		e := byID[id]
		if e == nil || e.src == nil {
			return nil, rejected("email", fmt.Errorf("%w: email %s", ErrNotFound, id))
		}
		out = append(out, e)
	}
	return out, nil
}

// gmPlan is the server work for one SetMailboxes / Move call.
type gmPlan struct {
	addLabel    map[string][]*gmEmail // label → All Mail emails
	removeLabel map[string][]*gmEmail
	toTrash     []*gmEmail
	toSpam      []*gmEmail
	restore     []*gmEmail          // out of Trash / Spam into All Mail
	restoreTo   map[string][]string // email id → labels it gets back
}

func newPlan() *gmPlan {
	return &gmPlan{addLabel: map[string][]*gmEmail{}, removeLabel: map[string][]*gmEmail{}, restoreTo: map[string][]string{}}
}

// plan decides what one email needs for adding and removing mailboxes.
func (p *gmPlan) plan(mb *gmMailboxes, e *gmEmail, add, remove []*model.IndexMailbox) {
	var addL, remL []string
	archive, toTrash, toSpam, leave := false, false, false, false
	for _, m := range add {
		switch {
		case m == mb.trash:
			toTrash = true
		case mb.spam != nil && m == mb.spam:
			toSpam = true
		case m.Virtual && m.Label == "":
			archive = true
		default:
			addL = append(addL, m.Label)
		}
	}
	for _, m := range remove {
		switch {
		case m == e.src:
			leave = true
		case m.Virtual && m.Label != "":
			remL = append(remL, m.Label)
		}
	}
	if archive {
		remL = append(remL, gmInbox)
	}
	inAllMail := e.src == mb.lr.allMail
	switch {
	case toTrash && e.src != mb.trash:
		p.toTrash = append(p.toTrash, e)
	case toSpam && e.src != mb.spam:
		p.toSpam = append(p.toSpam, e)
	case !inAllMail && (leave || len(addL) > 0 || archive):
		labels := addL
		if e.src == mb.trash {
			labels = append(slices.Clone(e.trashed), addL...)
		} else if len(labels) == 0 && !archive {
			labels = []string{gmInbox} // "not spam" goes to the inbox
		}
		labels = slices.DeleteFunc(labels, func(l string) bool { return slices.Contains(remL, l) })
		p.restore = append(p.restore, e)
		p.restoreTo[e.id] = dedupe(labels)
	case inAllMail:
		for _, l := range dedupe(addL) {
			if !slices.Contains(e.labels, l) {
				p.addLabel[l] = append(p.addLabel[l], e)
			}
		}
		for _, l := range dedupe(remL) {
			if slices.Contains(e.labels, l) && !slices.Contains(addL, l) {
				p.removeLabel[l] = append(p.removeLabel[l], e)
			}
		}
	}
}

func dedupe(s []string) []string {
	var out []string
	for _, x := range s {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// SetMailboxes adds and removes mailboxes (labels) on emails; see upstream.MailboxSetter.
func (g *Gmail) SetMailboxes(ctx context.Context, emailIDs []string, add, remove []string) (map[string]string, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return nil, err
	}
	addRows, err := mb.rows(add)
	if err != nil {
		return nil, err
	}
	remRows, err := mb.rows(remove)
	if err != nil {
		return nil, err
	}
	emails, err := g.loadEmails(mb, emailIDs)
	if err != nil {
		return nil, err
	}
	p := newPlan()
	for _, e := range emails {
		p.plan(mb, e, addRows, remRows)
	}
	return g.run(ctx, mb, p)
}

func (mb *gmMailboxes) rows(ids []string) ([]*model.IndexMailbox, error) {
	out := make([]*model.IndexMailbox, 0, len(ids))
	for _, id := range ids {
		m, err := mb.visible(id)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// Move puts emails in exactly one mailbox: the others are removed.
func (g *Gmail) Move(ctx context.Context, emailIDs []string, toMailboxID string) (map[string]string, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return nil, err
	}
	to, err := mb.visible(toMailboxID)
	if err != nil {
		return nil, err
	}
	emails, err := g.loadEmails(mb, emailIDs)
	if err != nil {
		return nil, err
	}
	p := newPlan()
	for _, e := range emails {
		var remove []*model.IndexMailbox
		if e.src != mb.lr.allMail && e.src != to {
			remove = append(remove, e.src)
		}
		for _, l := range e.labels {
			if m := mb.lr.byLabel[l]; m != nil && m != to {
				remove = append(remove, m)
			}
		}
		p.plan(mb, e, []*model.IndexMailbox{to}, remove)
	}
	return g.run(ctx, mb, p)
}

// Copy adds a mailbox (label) to emails. On Gmail a copy is the same message
// with one more label, so the returned ids are the emails' own.
func (g *Gmail) Copy(ctx context.Context, emailIDs []string, toMailboxID string) ([]string, map[string]string, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return nil, nil, err
	}
	to, err := mb.visible(toMailboxID)
	if err != nil {
		return nil, nil, err
	}
	var states map[string]string
	if !to.Virtual {
		states, err = g.Move(ctx, emailIDs, toMailboxID) // Trash / Spam hold the only copy
	} else {
		states, err = g.SetMailboxes(ctx, emailIDs, []string{toMailboxID}, nil)
	}
	return slices.Clone(emailIDs), states, err
}

// run executes a plan and refreshes the index from the server.
func (g *Gmail) run(ctx context.Context, mb *gmMailboxes, p *gmPlan) (map[string]string, error) {
	return g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		all := mb.lr.allMail
		touched := map[string]*gmEmail{} // All Mail emails whose labels changed
		if len(p.addLabel) > 0 || len(p.removeLabel) > 0 {
			if err := selectRW(ctx, c, all); err != nil {
				return err
			}
			for _, l := range sortedKeys(p.addLabel) {
				if err := storeLabels(ctx, c, p.addLabel[l], "+X-GM-LABELS", []string{l}); err != nil {
					return err
				}
				for _, e := range p.addLabel[l] {
					touched[e.id] = e
				}
			}
			for _, l := range sortedKeys(p.removeLabel) {
				if err := storeLabels(ctx, c, p.removeLabel[l], "-X-GM-LABELS", []string{l}); err != nil {
					return err
				}
				for _, e := range p.removeLabel[l] {
					touched[e.id] = e
				}
			}
		}
		if err := g.moveOut(ctx, c, states, p.toTrash, mb.trash, true); err != nil {
			return err
		}
		if err := g.moveOut(ctx, c, states, p.toSpam, mb.spam, false); err != nil {
			return err
		}
		if err := g.restore(ctx, c, states, mb, p, touched); err != nil {
			return err
		}
		return g.refreshLabels(ctx, c, states, mb, touched)
	})
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func storeLabels(ctx context.Context, c *imapc.Client, emails []*gmEmail, mode string, labels []string) error {
	uids := make(imapc.UIDSet, len(emails))
	for i, e := range emails {
		uids[i] = e.uid
	}
	_, err := c.Execute(ctx, "UID STORE", nil, imapc.Atom(uids.Compact()), imapc.Atom(mode+".SILENT"), labelArgs(labels))
	return err
}

// moveOut moves emails to Trash (remembering their labels) or Spam, keeping ids.
func (g *Gmail) moveOut(ctx context.Context, c *imapc.Client, states map[string]string, emails []*gmEmail, dest *model.IndexMailbox, remember bool) error {
	if len(emails) == 0 {
		return nil
	}
	if dest == nil {
		return rejected("move", errors.New("the account has no Spam folder"))
	}
	bySrc := map[*model.IndexMailbox][]*gmEmail{}
	for _, e := range emails {
		bySrc[e.src] = append(bySrc[e.src], e)
	}
	for src, list := range bySrc {
		if err := selectRW(ctx, c, src); err != nil {
			return err
		}
		uids := make(imapc.UIDSet, len(list))
		for i, e := range list {
			uids[i] = e.uid
		}
		cu, err := c.Move(ctx, uids, dest.Name)
		if err != nil {
			return err
		}
		var mapping map[uint32]uint32
		if cu != nil {
			mapping = cu.Map()
		}
		newUIDs, err := g.uidsIn(ctx, c, dest.Name, list, mapping)
		if err != nil {
			return err
		}
		if err := g.commit(states, func(tx *mailindex.Tx) error {
			for _, e := range list {
				to, ok := newUIDs[e.id]
				if !ok {
					continue // not found again; the next sync settles it
				}
				if err := tx.SetVirtual(e.id, 0, nil); err != nil {
					return err
				}
				if err := tx.Rebind(e.id, src.ID, e.uid, dest.ID, to); err != nil {
					return err
				}
				var keep []string
				if remember {
					keep = e.labels
					if src.Role == "trash" || src.Role == "junk" {
						keep = e.trashed
					}
					if keep == nil {
						keep = []string{}
					}
				}
				if err := tx.SetTrashLabels(e.id, keep); err != nil {
					return err
				}
				e.src, e.uid, e.labels = dest, to, nil
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// uidsIn finds emails in a folder after a move: from COPYUID when the server
// gave one, else by X-GM-MSGID.
func (g *Gmail) uidsIn(ctx context.Context, c *imapc.Client, folder string, list []*gmEmail, mapping map[uint32]uint32) (map[string]uint32, error) {
	out := map[string]uint32{}
	var missing []*gmEmail
	for _, e := range list {
		if to, ok := mapping[e.uid]; ok && to != 0 {
			out[e.id] = to
		} else {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	found, err := searchMsgIDs(ctx, c, folder, missing)
	for id, uid := range found {
		out[id] = uid
	}
	return out, err
}

// searchMsgIDs selects folder and finds each email by X-GM-MSGID. This is how
// a message is found in All Mail after APPEND or a move into a label folder:
// a UID from another folder means nothing there (jmap-perl#125's first trap).
func searchMsgIDs(ctx context.Context, c *imapc.Client, folder string, list []*gmEmail) (map[string]uint32, error) {
	if sel := c.Selected(); sel == nil || sel.Name != folder {
		if _, err := c.Select(ctx, folder, imapc.SelectOptions{}); err != nil {
			return nil, err
		}
	}
	out := map[string]uint32{}
	for _, e := range list {
		if e.msgID == "" {
			continue
		}
		uids, err := c.SearchUIDs(ctx, imapc.Atom("X-GM-MSGID"), imapc.Atom(e.msgID))
		if err != nil {
			return out, err
		}
		if len(uids) > 0 {
			out[e.id] = uids[0]
		}
	}
	return out, nil
}

// restore takes emails out of Trash / Spam: MOVE to the first label's folder
// (or All Mail), then the rest of the labels on All Mail.
func (g *Gmail) restore(ctx context.Context, c *imapc.Client, states map[string]string, mb *gmMailboxes, p *gmPlan, touched map[string]*gmEmail) error {
	if len(p.restore) == 0 {
		return nil
	}
	all := mb.lr.allMail
	type key struct {
		src  *model.IndexMailbox
		dest string
	}
	groups := map[key][]*gmEmail{}
	for _, e := range p.restore {
		dest := all.Name
		if ls := p.restoreTo[e.id]; len(ls) > 0 {
			if m := mb.lr.byLabel[ls[0]]; m != nil {
				dest = m.Name
			}
		}
		k := key{e.src, dest}
		groups[k] = append(groups[k], e)
	}
	for k, list := range groups {
		if err := selectRW(ctx, c, k.src); err != nil {
			return err
		}
		uids := make(imapc.UIDSet, len(list))
		for i, e := range list {
			uids[i] = e.uid
		}
		cu, err := c.Move(ctx, uids, k.dest)
		if err != nil {
			return err
		}
		var mapping map[uint32]uint32
		if cu != nil && k.dest == all.Name {
			mapping = cu.Map() // only All Mail UIDs are usable
		}
		newUIDs, err := g.uidsIn(ctx, c, all.Name, list, mapping)
		if err != nil {
			return err
		}
		if err := g.commit(states, func(tx *mailindex.Tx) error {
			for _, e := range list {
				to, ok := newUIDs[e.id]
				if !ok {
					continue
				}
				if err := tx.Rebind(e.id, k.src.ID, e.uid, all.ID, to); err != nil {
					return err
				}
				if err := tx.SetTrashLabels(e.id, nil); err != nil {
					return err
				}
				e.src, e.uid, e.labels = all, to, nil
				touched[e.id] = e
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// The other labels, on All Mail.
	if err := selectRW(ctx, c, all); err != nil {
		return err
	}
	byLabel := map[string][]*gmEmail{}
	for _, e := range p.restore {
		if e.src != all {
			continue
		}
		for i, l := range p.restoreTo[e.id] {
			if i == 0 && mb.lr.byLabel[l] != nil {
				continue // moved into its folder already
			}
			byLabel[l] = append(byLabel[l], e)
		}
	}
	for _, l := range sortedKeys(byLabel) {
		if err := storeLabels(ctx, c, byLabel[l], "+X-GM-LABELS", []string{l}); err != nil {
			return err
		}
	}
	return nil
}

// refreshLabels reads flags and labels of changed All Mail messages back and
// applies them, so the index shows what Gmail made of the change.
func (g *Gmail) refreshLabels(ctx context.Context, c *imapc.Client, states map[string]string, mb *gmMailboxes, touched map[string]*gmEmail) error {
	if len(touched) == 0 {
		return nil
	}
	all := mb.lr.allMail
	if sel := c.Selected(); sel == nil || sel.Name != all.Name {
		if _, err := c.Select(ctx, all.Name, imapc.SelectOptions{}); err != nil {
			return err
		}
	}
	byUID := map[uint32]*gmEmail{}
	uids := imapc.UIDSet{}
	for _, e := range touched {
		byUID[e.uid] = e
		uids = append(uids, e.uid)
	}
	var msgs []*imapc.Message
	err := c.Fetch(ctx, uids.Compact(), g.flagItems("UID", "FLAGS", "MODSEQ"), imapc.FetchOptions{UID: true}, func(m *imapc.Message) {
		if m.UID != 0 {
			msgs = append(msgs, m)
		}
	}, nil)
	if err != nil {
		return err
	}
	return g.commit(states, func(tx *mailindex.Tx) error {
		lr, err := loadLabelRows(tx)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			e := byUID[m.UID]
			if e == nil {
				continue
			}
			if err := tx.SetKeywords(all.ID, m.UID, mailindex.FlagsToKeywords(m.Flags), m.ModSeq); err != nil {
				return err
			}
			labels, _ := gmLabels(m)
			if err := g.setLabels(tx, lr, all, e.id, m.UID, labels); err != nil {
				return err
			}
		}
		return nil
	})
}

// Destroy deletes emails for good: into Trash first (EXPUNGE in All Mail or a
// label folder would only remove a label), then \Deleted + EXPUNGE there.
func (g *Gmail) Destroy(ctx context.Context, emailIDs []string) (map[string]string, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return nil, err
	}
	emails, err := g.loadEmails(mb, emailIDs)
	if err != nil {
		return nil, err
	}
	return g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		var toTrash []*gmEmail
		for _, e := range emails {
			if e.src == mb.lr.allMail {
				toTrash = append(toTrash, e)
			}
		}
		if err := g.moveOut(ctx, c, states, toTrash, mb.trash, false); err != nil {
			return err
		}
		bySrc := map[*model.IndexMailbox][]*gmEmail{}
		for _, e := range emails {
			if e.src != mb.lr.allMail {
				bySrc[e.src] = append(bySrc[e.src], e)
			}
		}
		for src, list := range bySrc {
			if err := selectRW(ctx, c, src); err != nil {
				return err
			}
			uids := make(imapc.UIDSet, len(list))
			for i, e := range list {
				uids[i] = e.uid
			}
			if err := c.Store(ctx, uids, imapc.StoreAdd, []string{`\Deleted`}, nil); err != nil {
				return err
			}
			if err := c.UIDExpunge(ctx, uids); err != nil {
				return err
			}
			if err := g.commit(states, func(tx *mailindex.Tx) error { return tx.RemoveUIDs(src.ID, uids) }); err != nil {
				return err
			}
		}
		return nil
	})
}

// Append stores a message in a mailbox. The APPENDUID UID belongs to the folder
// appended to; the message is found in All Mail by its X-GM-MSGID and indexed
// there with its labels.
func (g *Gmail) Append(ctx context.Context, mailboxID string, raw []byte, keywords []string, receivedAt time.Time) (string, map[string]string, error) {
	mb, err := g.loadMailboxes()
	if err != nil {
		return "", nil, err
	}
	to, err := mb.visible(mailboxID)
	if err != nil {
		return "", nil, err
	}
	folder := to.Name
	if to.Virtual && to.Label == "" {
		folder = mb.lr.allMail.Name // Archive: no label
	}
	var id string
	states, err := g.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		au, err := c.Append(ctx, folder, flagsOf(keywords), receivedAt, raw)
		if err != nil {
			return err
		}
		home := to // where the message is indexed: All Mail, or Trash / Spam
		if to.Virtual {
			home = mb.lr.allMail
		}
		var uid uint32
		if au != nil && folder == home.Name {
			uid = au.UID
		} else {
			msgID, err := appendedMsgID(ctx, c, folder, au, raw)
			if err != nil {
				return err
			}
			found, err := searchMsgIDs(ctx, c, home.Name, []*gmEmail{{id: "new", msgID: msgID}})
			if err != nil {
				return err
			}
			if uid = found["new"]; uid == 0 {
				return rejected("append", errors.New("appended message not found in "+home.Name))
			}
		}
		ids, err := g.importUIDs(ctx, c, states, home, []uint32{uid})
		id = ids[uid]
		return err
	})
	return id, states, err
}

// appendedMsgID reads the X-GM-MSGID of a message just appended to folder:
// by its APPENDUID UID there, else by its Message-ID header.
func appendedMsgID(ctx context.Context, c *imapc.Client, folder string, au *imapc.AppendUID, raw []byte) (string, error) {
	if _, err := c.Select(ctx, folder, imapc.SelectOptions{ReadOnly: true}); err != nil {
		return "", err
	}
	set := ""
	if au != nil {
		set = strconv.FormatUint(uint64(au.UID), 10)
	} else if mid := firstID(headerValue(raw, "Message-Id")); mid != "" {
		uids, err := c.SearchUIDs(ctx, imapc.Atom("HEADER"), imapc.Atom("Message-ID"), "<"+mid+">")
		if err != nil || len(uids) == 0 {
			return "", rejected("append", fmt.Errorf("appended message not found in %s", folder))
		}
		set = strconv.FormatUint(uint64(uids[len(uids)-1]), 10)
	} else {
		return "", rejected("append", errors.New("no APPENDUID and no Message-ID"))
	}
	var msgID string
	err := c.Fetch(ctx, set, []string{"UID", "X-GM-MSGID"}, imapc.FetchOptions{UID: true}, func(m *imapc.Message) {
		if k := gmKey(m, "X-GM-MSGID"); k != "" {
			msgID = strings.TrimPrefix(k, "gm:")
		}
	}, nil)
	if err == nil && msgID == "" {
		err = rejected("append", errors.New("no X-GM-MSGID for the appended message"))
	}
	return msgID, err
}
