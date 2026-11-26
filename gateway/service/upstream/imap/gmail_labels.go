package imap

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// Gmail labels as X-GM-LABELS shows them.
const (
	gmInbox     = `\Inbox`
	gmSent      = `\Sent`
	gmDraft     = `\Draft`
	gmStarred   = `\Starred`
	gmImportant = `\Important`
	gmTrash     = `\Trash`
	gmSpam      = `\Spam`
)

// gmArchiveLeaf is the last name segment of the virtual Archive mailbox; it sits
// under Gmail's reserved [Gmail] prefix, where no user label can be.
const gmArchiveLeaf = "Archive"

// canonicalLabel normalises a system label's case and maps \Drafts (the
// folder's SPECIAL-USE attribute, which some clients send as a label) to \Draft
// (what X-GM-LABELS shows): jmap-perl#125's third trap.
func canonicalLabel(l string) string {
	if !strings.HasPrefix(l, `\`) {
		return l
	}
	switch strings.ToLower(l) {
	case `\inbox`:
		return gmInbox
	case `\sent`:
		return gmSent
	case `\draft`, `\drafts`:
		return gmDraft
	case `\starred`, `\flagged`:
		return gmStarred
	case `\important`:
		return gmImportant
	case `\trash`:
		return gmTrash
	case `\spam`, `\junk`:
		return gmSpam
	}
	return l
}

// hiddenLabel reports labels that are not mailboxes: \Starred is the $flagged
// keyword, \Important and Gmail's categories / smart labels (^...) are hidden,
// \Trash and \Spam are real folders.
func hiddenLabel(l string) bool {
	switch l {
	case gmStarred, gmImportant, gmTrash, gmSpam:
		return true
	}
	return strings.HasPrefix(l, "^") || strings.HasPrefix(strings.ToUpper(l), "CATEGORY_")
}

// gmLabels reads X-GM-LABELS of a fetched message; ok is false when the item
// was not in the response.
func gmLabels(m *imapc.Message) (labels []string, ok bool) {
	v, ok := m.Extra["X-GM-LABELS"]
	if !ok {
		return nil, false
	}
	for _, x := range v.List {
		s := x.String()
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, `\`) {
			s = imapc.DecodeMailbox(s)
		}
		labels = append(labels, canonicalLabel(s))
	}
	// \Draft is also the message flag; trust either.
	if slices.ContainsFunc(m.Flags, func(f string) bool { return strings.EqualFold(f, `\Draft`) }) && !slices.Contains(labels, gmDraft) {
		labels = append(labels, gmDraft)
	}
	return labels, true
}

// labelArg is one label as a STORE / SEARCH argument: system labels as atoms,
// user labels as quoted strings (modified UTF-7), so "Work Projects" stays one
// label: jmap-perl#125's second trap.
func labelArg(l string) any {
	if strings.HasPrefix(l, `\`) {
		return imapc.Atom(l)
	}
	return imapc.MailboxName(l)
}

func labelArgs(labels []string) []any {
	out := make([]any, len(labels))
	for i, l := range labels {
		out[i] = labelArg(l)
	}
	return out
}

// gmailEntry sorts a LIST entry: a folder the engine syncs (All Mail, Trash,
// Spam), a label mailbox, or nothing.
type gmailEntry struct {
	sync   bool
	hidden bool   // All Mail
	label  string // label mailboxes
	role   string
}

func classifyGmail(e *imapc.ListEntry) (gmailEntry, bool) {
	switch {
	case e.HasAttr(`\All`):
		return gmailEntry{sync: true, hidden: true, role: "all"}, true
	case e.HasAttr(`\Trash`):
		return gmailEntry{sync: true, role: "trash"}, true
	case e.HasAttr(`\Junk`):
		return gmailEntry{sync: true, role: "junk"}, true
	case noSelect(e), e.HasAttr(`\Flagged`), e.HasAttr(`\Important`):
		return gmailEntry{}, false
	case strings.EqualFold(e.Name, "INBOX"):
		return gmailEntry{label: gmInbox, role: "inbox"}, true
	case e.HasAttr(`\Sent`):
		return gmailEntry{label: gmSent, role: "sent"}, true
	case e.HasAttr(`\Drafts`):
		return gmailEntry{label: gmDraft, role: "drafts"}, true
	}
	return gmailEntry{label: e.Name}, true
}

// labelRows indexes the virtual mailboxes of a Gmail index.
type labelRows struct {
	byLabel map[string]*model.IndexMailbox
	archive *model.IndexMailbox
	allMail *model.IndexMailbox
}

func loadLabelRows(tx *mailindex.Tx) (*labelRows, error) {
	all, err := tx.Mailboxes()
	if err != nil {
		return nil, err
	}
	lr := &labelRows{byLabel: map[string]*model.IndexMailbox{}}
	for i := range all {
		m := &all[i]
		switch {
		case m.Hidden:
			lr.allMail = m
		case m.Virtual && m.Label == "":
			lr.archive = m
		case m.Virtual:
			lr.byLabel[m.Label] = m
		}
	}
	return lr, nil
}

// mailboxesFor maps an All Mail message's labels to mailbox ids. A label LIST
// has not shown is skipped: it is hidden from IMAP in Gmail's settings, or new
// since the last LIST (then the next LIST adds it and relabels every message).
// No visible label means Archive.
func (lr *labelRows) mailboxesFor(tx *mailindex.Tx, labels []string) ([]string, error) {
	var out []string
	for _, l := range labels {
		l = canonicalLabel(l)
		if hiddenLabel(l) {
			continue
		}
		row := lr.byLabel[l]
		if row == nil {
			continue
		}
		if !slices.Contains(out, row.ID) {
			out = append(out, row.ID)
		}
	}
	if len(out) == 0 && lr.archive != nil {
		out = append(out, lr.archive.ID)
	}
	return out, nil
}

// create adds a label mailbox under its parent label, if that exists.
func (lr *labelRows) create(tx *mailindex.Tx, label, delim, role string) (*model.IndexMailbox, error) {
	m := &model.IndexMailbox{Name: label, Delimiter: delim, Role: role, SortOrder: sortOrder(role), Virtual: true, Label: label, Subscribed: true, Attrs: "[]"}
	if p := lr.byLabel[parentName(label, delim)]; p != nil {
		m.ParentID = p.ID
	}
	if err := tx.CreateMailbox(m); err != nil {
		return nil, err
	}
	lr.byLabel[label] = m
	return m, nil
}

// setLabels gives an email the mailboxes of its labels (All Mail) or none
// (Trash, Spam: the folder membership is all).
func (u *Upstream) setLabels(tx *mailindex.Tx, lr *labelRows, src *model.IndexMailbox, emailID string, uid uint32, labels []string) error {
	if !src.Hidden {
		return tx.SetVirtual(emailID, uid, nil)
	}
	ids, err := lr.mailboxesFor(tx, labels)
	if err != nil {
		return err
	}
	return tx.SetVirtual(emailID, uid, ids)
}

// reconcileLabels brings the label mailboxes in line with LIST: one per label
// folder, plus Archive. A label that is gone loses its mailbox; its emails stay
// (in All Mail) and land in Archive when it was their last label.
func (u *Upstream) reconcileLabels(tx *mailindex.Tx, entries []*imapc.ListEntry, kinds []gmailEntry, allMailName string) error {
	lr, err := loadLabelRows(tx)
	if err != nil {
		return err
	}
	changed := false
	defer func() {
		// A label appeared or went (also a rename in Gmail's web UI): Gmail does
		// not bump the messages' MODSEQ for that, so re-read every label.
		if changed && tx.Ready() {
			u.gm.relabel.Store(true)
		}
	}()
	seen := map[string]bool{}
	type want struct {
		e *imapc.ListEntry
		k gmailEntry
	}
	var list []want
	for i, e := range entries {
		if kinds[i].label != "" {
			list = append(list, want{e, kinds[i]})
		}
	}
	// Parents first, so children find them.
	slices.SortFunc(list, func(a, b want) int { return strings.Compare(a.e.Name, b.e.Name) })
	for _, w := range list {
		seen[w.k.label] = true
		attrs, _ := json.Marshal(w.e.Attrs)
		row := lr.byLabel[w.k.label]
		if row == nil {
			row, err = lr.create(tx, w.k.label, w.e.Delimiter, w.k.role)
			if err != nil {
				return err
			}
			changed = true
		}
		parent := ""
		if w.k.role == "" {
			if p := lr.byLabel[parentName(w.e.Name, w.e.Delimiter)]; p != nil {
				parent = p.ID
			}
		}
		before := *row
		row.Name, row.Delimiter, row.Role, row.ParentID, row.Attrs, row.SortOrder = w.e.Name, w.e.Delimiter, w.k.role, parent, string(attrs), sortOrder(w.k.role)
		if before != *row {
			visible := before.Name != row.Name || before.ParentID != row.ParentID || before.Role != row.Role
			if err := tx.UpdateMailbox(row, visible); err != nil {
				return err
			}
		}
	}
	if lr.archive == nil {
		name := gmArchiveLeaf
		if i := strings.LastIndex(allMailName, "/"); i > 0 {
			name = allMailName[:i+1] + gmArchiveLeaf
		}
		m := &model.IndexMailbox{Name: name, Delimiter: "/", Role: "archive", SortOrder: sortOrder("archive"), Virtual: true, Subscribed: true, Attrs: "[]"}
		if err := tx.CreateMailbox(m); err != nil {
			return err
		}
		lr.archive = m
	}
	for label, row := range lr.byLabel {
		if seen[label] {
			continue
		}
		if err := u.dropLabelRow(tx, lr, row); err != nil {
			return err
		}
		changed = true
	}
	return nil
}

// dropLabelRow removes a label mailbox; emails it leaves without a visible label
// go to Archive.
func (u *Upstream) dropLabelRow(tx *mailindex.Tx, lr *labelRows, row *model.IndexMailbox) error {
	var ms []model.IndexMembership
	if err := tx.DB().Where("mailbox_id = ?", row.ID).Find(&ms).Error; err != nil {
		return err
	}
	// Emails still in All Mail are kept and logged as updated.
	if err := tx.DestroyMailbox(row.ID); err != nil {
		return err
	}
	delete(lr.byLabel, row.Label)
	for _, m := range ms {
		cur, err := tx.VirtualMailboxes(m.EmailID)
		if err != nil {
			return err
		}
		if len(cur) == 0 && lr.archive != nil {
			if err := tx.SetVirtual(m.EmailID, m.UID, []string{lr.archive.ID}); err != nil {
				return err
			}
		}
	}
	return nil
}
