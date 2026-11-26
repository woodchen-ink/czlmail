package mailindex

import (
	"errors"
	"maps"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Tx is one write transaction. Its methods change index rows and record the
// matching changelog entries; nothing is logged before the first import finished.
type Tx struct {
	db      *gorm.DB
	meta    model.IndexMeta
	pending map[changeKey]string
	order   []changeKey
	// delivered are emails the sync engine found new on the server, for push.
	delivered []string
}

type changeKey struct{ typ, id string }

// Write runs fn in one transaction and commits its changelog entries with it. It
// returns the new state of every type that changed (nil when none, or before the
// first import finished).
func (s *Store) Write(fn func(*Tx) error) (map[string]string, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var tx *Tx
	err := s.db.Transaction(func(db *gorm.DB) error {
		tx = &Tx{db: db, meta: s.Meta(), pending: map[changeKey]string{}}
		if err := fn(tx); err != nil {
			return err
		}
		return tx.flush()
	})
	if err != nil {
		return nil, err
	}
	before := s.Meta()
	s.mu.Lock()
	s.meta = tx.meta
	s.mu.Unlock()
	if !tx.meta.Ready {
		return nil, nil
	}
	if len(tx.delivered) > 0 {
		s.mu.RLock()
		hook := s.onDelivered
		s.mu.RUnlock()
		if hook != nil {
			hook(tx.delivered)
		}
	}
	var changed map[string]string
	for _, t := range Types {
		if !before.Ready || typeSeq(&before, t) != typeSeq(&tx.meta, t) {
			if changed == nil {
				changed = map[string]string{}
			}
			changed[t] = formatState(tx.meta.Generation, typeSeq(&tx.meta, t))
		}
	}
	if changed != nil {
		s.mu.RLock()
		hook := s.onCommit
		s.mu.RUnlock()
		if hook != nil {
			hook(maps.Clone(changed))
		}
	}
	return changed, nil
}

// DB is the transaction's database handle, for reads inside the transaction.
func (tx *Tx) DB() *gorm.DB { return tx.db }

// Ready reports whether the index has a state (changes are being logged).
func (tx *Tx) Ready() bool { return tx.meta.Ready }

// SetReady marks the first import finished; the states start at "<gen>-0".
func (tx *Tx) SetReady() { tx.meta.Ready = true }

// Delivered marks an email created in this transaction as newly arrived on the
// server (not created by a gateway write), so the OnDelivered hook sees it once
// the transaction commits. Nothing is reported before the first import finished.
func (tx *Tx) Delivered(emailID string) {
	if tx.meta.Ready {
		tx.delivered = append(tx.delivered, emailID)
	}
}

// SetLastFullSync records a nightly full compare.
func (tx *Tx) SetLastFullSync(t time.Time) { tx.meta.LastFullSync = &t }

// record merges a change into this transaction's pending entries.
func (tx *Tx) record(typ, id, kind string) {
	if !tx.meta.Ready {
		return
	}
	k := changeKey{typ, id}
	prev, ok := tx.pending[k]
	if !ok {
		tx.pending[k] = kind
		tx.order = append(tx.order, k)
		return
	}
	switch {
	case prev == KindCreated && kind == KindDestroyed:
		tx.pending[k] = "" // never seen by anyone: drop
	case prev == KindCreated:
		// stays created
	case kind == KindDestroyed:
		tx.pending[k] = KindDestroyed
	case prev == KindDestroyed:
		// destroyed stays destroyed (ids are never reused)
	case prev == KindCounts && kind == KindUpdated:
		tx.pending[k] = KindUpdated
	}
}

func (tx *Tx) flush() error {
	now := time.Now().UTC()
	rows := make([]model.IndexChange, 0, len(tx.order))
	for _, k := range tx.order {
		kind := tx.pending[k]
		if kind == "" {
			continue
		}
		tx.meta.Seq++
		rows = append(rows, model.IndexChange{Seq: tx.meta.Seq, Type: k.typ, ObjectID: k.id, Kind: kind, CreatedAt: now})
		switch k.typ {
		case TypeMailbox:
			tx.meta.MailboxSeq = tx.meta.Seq
		case TypeEmail:
			tx.meta.EmailSeq = tx.meta.Seq
		case TypeThread:
			tx.meta.ThreadSeq = tx.meta.Seq
		}
	}
	if len(rows) > 0 {
		if err := tx.db.CreateInBatches(rows, 500).Error; err != nil {
			return err
		}
	}
	tx.meta.UpdatedAt = now
	return tx.db.Save(&tx.meta).Error
}

// Mailboxes returns every mailbox.
func (tx *Tx) Mailboxes() ([]model.IndexMailbox, error) {
	var out []model.IndexMailbox
	err := tx.db.Order("name").Find(&out).Error
	return out, err
}

// CreateMailbox inserts a mailbox (allocating its id when empty).
func (tx *Tx) CreateMailbox(m *model.IndexMailbox) error {
	if m.ID == "" {
		m.ID = NewMailboxID()
	}
	if err := tx.db.Create(m).Error; err != nil {
		return err
	}
	tx.record(TypeMailbox, m.ID, KindCreated)
	return nil
}

// UpdateMailbox saves a mailbox. visible logs an update for clients (name,
// parent, role, subscription); sync-point-only saves pass false.
func (tx *Tx) UpdateMailbox(m *model.IndexMailbox, visible bool) error {
	if err := tx.db.Save(m).Error; err != nil {
		return err
	}
	if visible {
		tx.record(TypeMailbox, m.ID, KindUpdated)
	}
	return nil
}

// DestroyMailbox removes a mailbox and destroys the emails only it contained.
func (tx *Tx) DestroyMailbox(id string) error {
	var ids []string
	if err := tx.db.Model(&model.IndexMembership{}).Where("mailbox_id = ?", id).Pluck("email_id", &ids).Error; err != nil {
		return err
	}
	if err := tx.db.Where("mailbox_id = ?", id).Delete(&model.IndexMembership{}).Error; err != nil {
		return err
	}
	for _, eid := range ids {
		if err := tx.destroyIfUnplaced(eid, id); err != nil {
			return err
		}
	}
	// Orphans that last lived here have nowhere to go back to.
	var orphans []string
	if err := tx.db.Model(&model.IndexEmail{}).Where("orphaned_from = ?", id).Pluck("id", &orphans).Error; err != nil {
		return err
	}
	for _, eid := range orphans {
		if err := tx.DestroyEmail(eid); err != nil {
			return err
		}
	}
	if err := tx.db.Delete(&model.IndexMailbox{}, "id = ?", id).Error; err != nil {
		return err
	}
	tx.record(TypeMailbox, id, KindDestroyed)
	return nil
}

// Membership is one (mailbox, uid) of an email.
type Membership = model.IndexMembership

// MailboxUIDs returns uid → membership for a mailbox.
func (tx *Tx) MailboxUIDs(mailboxID string) (map[uint32]Membership, error) {
	var rows []Membership
	if err := tx.db.Where("mailbox_id = ?", mailboxID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint32]Membership, len(rows))
	for _, r := range rows {
		out[r.UID] = r
	}
	return out, nil
}

// HasUID reports whether (mailbox, uid) is indexed.
func (tx *Tx) HasUID(mailboxID string, uid uint32) (bool, error) {
	var n int64
	err := tx.db.Model(&Membership{}).Where("mailbox_id = ? AND uid = ?", mailboxID, uid).Count(&n).Error
	return n > 0, err
}

// CreateEmail inserts a new email in one mailbox. e.ID is allocated when empty;
// e.ThreadID is resolved from UpstreamThreadID or the references when empty.
// refs are the Message-IDs from In-Reply-To and References.
func (tx *Tx) CreateEmail(e *model.IndexEmail, refs []string, mailboxID string, uid uint32, modseq uint64) error {
	if e.ID == "" {
		e.ID = NewEmailID()
	}
	threadExisted := false
	if e.ThreadID == "" {
		tid, err := tx.findThread(e, refs)
		if err != nil {
			return err
		}
		if tid != "" {
			e.ThreadID, threadExisted = tid, true
		} else {
			e.ThreadID = NewThreadID()
		}
	} else {
		var n int64
		if err := tx.db.Model(&model.IndexEmail{}).Where("thread_id = ?", e.ThreadID).Count(&n).Error; err != nil {
			return err
		}
		threadExisted = n > 0
	}
	if e.Keywords == "" {
		e.Keywords = "{}"
	}
	if err := tx.db.Create(e).Error; err != nil {
		return err
	}
	if err := tx.db.Create(&Membership{MailboxID: mailboxID, UID: uid, EmailID: e.ID, ModSeq: modseq}).Error; err != nil {
		return err
	}
	if err := tx.addRefs(e.ID, refs); err != nil {
		return err
	}
	tx.record(TypeEmail, e.ID, KindCreated)
	if threadExisted {
		tx.record(TypeThread, e.ThreadID, KindUpdated)
	} else {
		tx.record(TypeThread, e.ThreadID, KindCreated)
	}
	tx.record(TypeMailbox, mailboxID, KindCounts)
	return nil
}

func (tx *Tx) addRefs(emailID string, refs []string) error {
	seen := map[string]bool{}
	rows := make([]model.IndexEmailRef, 0, len(refs))
	for _, r := range refs {
		if r == "" || seen[r] || len(r) > 998 {
			continue
		}
		seen[r] = true
		rows = append(rows, model.IndexEmailRef{MessageID: r, EmailID: emailID})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// findThread returns the thread of an email this one references or that
// references this one, or "".
func (tx *Tx) findThread(e *model.IndexEmail, refs []string) (string, error) {
	var tid string
	if e.UpstreamThreadID != "" {
		err := tx.db.Model(&model.IndexEmail{}).Where("upstream_thread_id = ?", e.UpstreamThreadID).Limit(1).Pluck("thread_id", &tid).Error
		return tid, err
	}
	if len(refs) > 0 {
		var tids []string
		if err := tx.db.Model(&model.IndexEmail{}).Where("message_id IN ?", refs).Limit(1).Pluck("thread_id", &tids).Error; err != nil {
			return "", err
		}
		if len(tids) > 0 {
			return tids[0], nil
		}
	}
	if e.MessageID != "" {
		var tids []string
		err := tx.db.Table("email_refs r").Joins("JOIN emails e ON e.id = r.email_id").
			Where("r.message_id = ?", e.MessageID).Limit(1).Pluck("e.thread_id", &tids).Error
		if err != nil {
			return "", err
		}
		if len(tids) > 0 {
			return tids[0], nil
		}
	}
	return "", nil
}

// Detach removes memberships, leaving their emails orphaned (see
// model.IndexEmail.OrphanedFrom) until Attach or DestroyOrphans. Nothing is logged:
// the email either reappears unchanged or is destroyed later in the round.
func (tx *Tx) Detach(mailboxID string, uids []uint32) error {
	for chunk := range chunks(uids, 500) {
		var ids []string
		if err := tx.db.Model(&Membership{}).Where("mailbox_id = ? AND uid IN ?", mailboxID, chunk).Pluck("email_id", &ids).Error; err != nil {
			return err
		}
		if err := tx.db.Where("mailbox_id = ? AND uid IN ?", mailboxID, chunk).Delete(&Membership{}).Error; err != nil {
			return err
		}
		if err := tx.orphan(ids, mailboxID); err != nil {
			return err
		}
	}
	return nil
}

// DetachAll orphans every email of a mailbox (UIDVALIDITY changed).
func (tx *Tx) DetachAll(mailboxID string) error {
	var ids []string
	if err := tx.db.Model(&Membership{}).Where("mailbox_id = ?", mailboxID).Pluck("email_id", &ids).Error; err != nil {
		return err
	}
	if err := tx.db.Where("mailbox_id = ?", mailboxID).Delete(&Membership{}).Error; err != nil {
		return err
	}
	return tx.orphan(ids, mailboxID)
}

func (tx *Tx) orphan(ids []string, mailboxID string) error {
	for chunk := range chunks(ids, 500) {
		// Still in another real folder: only its mailboxIds changed. Only emails
		// left without a real membership become orphans; their virtual
		// memberships (Gmail labels) depended on the one that went.
		var placed []string
		if err := tx.db.Table("memberships m").Joins("JOIN mailboxes b ON b.id = m.mailbox_id").
			Where("m.email_id IN ? AND b.virtual = ?", chunk, false).Distinct().Pluck("m.email_id", &placed).Error; err != nil {
			return err
		}
		still := map[string]bool{}
		for _, id := range placed {
			still[id] = true
			tx.record(TypeEmail, id, KindUpdated)
		}
		if len(placed) > 0 {
			tx.record(TypeMailbox, mailboxID, KindCounts)
		}
		var gone []string
		for _, id := range chunk {
			if !still[id] {
				gone = append(gone, id)
			}
		}
		if len(gone) == 0 {
			continue
		}
		if err := tx.dropVirtual(gone); err != nil {
			return err
		}
		err := tx.db.Model(&model.IndexEmail{}).
			Where("id IN ? AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.email_id = emails.id)", gone).
			Update("orphaned_from", mailboxID).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// dropVirtual deletes the virtual memberships of emails, counting their mailboxes.
func (tx *Tx) dropVirtual(emailIDs []string) error {
	var virt []string
	if err := tx.db.Model(&model.IndexMailbox{}).Where("virtual = ?", true).Pluck("id", &virt).Error; err != nil || len(virt) == 0 {
		return err
	}
	var mboxes []string
	if err := tx.db.Model(&Membership{}).Where("email_id IN ? AND mailbox_id IN ?", emailIDs, virt).Distinct().Pluck("mailbox_id", &mboxes).Error; err != nil {
		return err
	}
	if len(mboxes) == 0 {
		return nil
	}
	for _, m := range mboxes {
		tx.record(TypeMailbox, m, KindCounts)
	}
	return tx.db.Where("email_id IN ? AND mailbox_id IN ?", emailIDs, virt).Delete(&Membership{}).Error
}

// VirtualMailboxes returns the virtual mailboxes an email is in.
func (tx *Tx) VirtualMailboxes(emailID string) ([]string, error) {
	var out []string
	err := tx.db.Table("memberships m").Joins("JOIN mailboxes b ON b.id = m.mailbox_id").
		Where("m.email_id = ? AND b.virtual = ?", emailID, true).Order("m.mailbox_id").Pluck("m.mailbox_id", &out).Error
	return out, err
}

// SetVirtual replaces an email's virtual memberships (Gmail labels) with
// mailboxIDs, under the source folder's uid. A change is an Email update and
// changes the counts of the mailboxes it entered or left.
func (tx *Tx) SetVirtual(emailID string, uid uint32, mailboxIDs []string) error {
	cur, err := tx.VirtualMailboxes(emailID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, m := range mailboxIDs {
		want[m] = true
	}
	have := map[string]bool{}
	changed := false
	for _, m := range cur {
		have[m] = true
		if !want[m] {
			if err := tx.db.Where("mailbox_id = ? AND email_id = ?", m, emailID).Delete(&Membership{}).Error; err != nil {
				return err
			}
			tx.record(TypeMailbox, m, KindCounts)
			changed = true
		}
	}
	for m := range want {
		if have[m] {
			continue
		}
		row := Membership{MailboxID: m, UID: uid, EmailID: emailID}
		if err := tx.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
			return err
		}
		tx.record(TypeMailbox, m, KindCounts)
		changed = true
	}
	if changed {
		tx.record(TypeEmail, emailID, KindUpdated)
	}
	return nil
}

// SetTrashLabels remembers the labels an email had before the gateway trashed it
// (nil clears them).
func (tx *Tx) SetTrashLabels(emailID string, labels []string) error {
	v := ""
	if labels != nil {
		v = EncodeJSON(labels)
	}
	return tx.db.Model(&model.IndexEmail{}).Where("id = ?", emailID).Update("trash_labels", v).Error
}

// Orphan is an email waiting to be re-attached.
type Orphan struct {
	ID         string
	UpstreamID string
	MessageID  string
	Size       int64
	ReceivedAt time.Time
	From       string // OrphanedFrom
}

// Orphans lists every orphaned email.
func (tx *Tx) Orphans() ([]Orphan, error) {
	var rows []model.IndexEmail
	err := tx.db.Select("id", "upstream_id", "message_id", "size", "received_at", "orphaned_from").
		Where("orphaned_from <> ''").Find(&rows).Error
	out := make([]Orphan, len(rows))
	for i, r := range rows {
		out[i] = Orphan{ID: r.ID, UpstreamID: r.UpstreamID, MessageID: r.MessageID, Size: r.Size, ReceivedAt: r.ReceivedAt, From: r.OrphanedFrom}
	}
	return out, err
}

// Attach gives an orphan its new place. Moving to another mailbox is an Email
// update (mailboxIds) and changes both mailboxes' counts; reappearing in the same
// mailbox under a new UID (UIDVALIDITY reset) is invisible to clients.
func (tx *Tx) Attach(o Orphan, mailboxID string, uid uint32, modseq uint64) error {
	if err := tx.db.Create(&Membership{MailboxID: mailboxID, UID: uid, EmailID: o.ID, ModSeq: modseq}).Error; err != nil {
		return err
	}
	if err := tx.db.Model(&model.IndexEmail{}).Where("id = ?", o.ID).Update("orphaned_from", "").Error; err != nil {
		return err
	}
	if o.From != mailboxID {
		tx.record(TypeEmail, o.ID, KindUpdated)
		tx.record(TypeMailbox, mailboxID, KindCounts)
		// The folder it left may be gone already (moved out, then deleted).
		var n int64
		if err := tx.db.Model(&model.IndexMailbox{}).Where("id = ?", o.From).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			tx.record(TypeMailbox, o.From, KindCounts)
		}
	}
	return nil
}

// Rebind moves a membership to another (mailbox, uid) after the gateway's own
// MOVE (COPYUID known): the email keeps its id.
func (tx *Tx) Rebind(emailID, fromMailbox string, fromUID uint32, toMailbox string, toUID uint32) error {
	if err := tx.db.Where("mailbox_id = ? AND uid = ?", fromMailbox, fromUID).Delete(&Membership{}).Error; err != nil {
		return err
	}
	if err := tx.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Membership{MailboxID: toMailbox, UID: toUID, EmailID: emailID}).Error; err != nil {
		return err
	}
	if err := tx.db.Model(&model.IndexEmail{}).Where("id = ? AND orphaned_from <> ''", emailID).Update("orphaned_from", "").Error; err != nil {
		return err
	}
	if fromMailbox != toMailbox {
		tx.record(TypeEmail, emailID, KindUpdated)
		tx.record(TypeMailbox, fromMailbox, KindCounts)
		tx.record(TypeMailbox, toMailbox, KindCounts)
	}
	return nil
}

// RemoveMailbox deletes a mailbox row whose emails were detached (the folder is
// gone upstream; its messages may turn up elsewhere in the same round).
func (tx *Tx) RemoveMailbox(id string) error {
	if err := tx.DetachAll(id); err != nil {
		return err
	}
	if err := tx.db.Delete(&model.IndexMailbox{}, "id = ?", id).Error; err != nil {
		return err
	}
	tx.record(TypeMailbox, id, KindDestroyed)
	return nil
}

// DestroyOrphans destroys every orphan still unplaced at the end of a round.
func (tx *Tx) DestroyOrphans() (int, error) {
	var ids []string
	err := tx.db.Model(&model.IndexEmail{}).
		Where("orphaned_from <> '' AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.email_id = emails.id)").
		Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	// Emails re-placed by a write (Rebind) only lose the marker.
	if err := tx.db.Model(&model.IndexEmail{}).Where("orphaned_from <> ''").Update("orphaned_from", "").Error; err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := tx.DestroyEmail(id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// RemoveUIDs deletes memberships and destroys emails left without one
// (the gateway's own EXPUNGE).
func (tx *Tx) RemoveUIDs(mailboxID string, uids []uint32) error {
	for chunk := range chunks(uids, 500) {
		var ids []string
		if err := tx.db.Model(&Membership{}).Where("mailbox_id = ? AND uid IN ?", mailboxID, chunk).Pluck("email_id", &ids).Error; err != nil {
			return err
		}
		if err := tx.db.Where("mailbox_id = ? AND uid IN ?", mailboxID, chunk).Delete(&Membership{}).Error; err != nil {
			return err
		}
		for _, id := range ids {
			if err := tx.destroyIfUnplaced(id, mailboxID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tx *Tx) destroyIfUnplaced(emailID, mailboxID string) error {
	var n int64
	if err := tx.db.Table("memberships m").Joins("JOIN mailboxes b ON b.id = m.mailbox_id").
		Where("m.email_id = ? AND b.virtual = ?", emailID, false).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		tx.record(TypeEmail, emailID, KindUpdated)
		tx.record(TypeMailbox, mailboxID, KindCounts)
		return nil
	}
	// Virtual memberships cannot outlive the real one.
	if err := tx.DestroyEmail(emailID); err != nil {
		return err
	}
	tx.record(TypeMailbox, mailboxID, KindCounts)
	return nil
}

// DestroyEmail deletes an email with its memberships and references.
func (tx *Tx) DestroyEmail(emailID string) error {
	var mboxes []string
	if err := tx.db.Model(&Membership{}).Where("email_id = ?", emailID).Pluck("mailbox_id", &mboxes).Error; err != nil {
		return err
	}
	if err := tx.db.Where("email_id = ?", emailID).Delete(&Membership{}).Error; err != nil {
		return err
	}
	return tx.destroy(emailID, mboxes)
}

func (tx *Tx) destroy(emailID string, mboxes []string) error {
	var e model.IndexEmail
	err := tx.db.Select("id", "thread_id", "orphaned_from").Where("id = ?", emailID).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.OrphanedFrom != "" {
		mboxes = append(mboxes, e.OrphanedFrom)
	}
	if err := tx.db.Where("email_id = ?", emailID).Delete(&model.IndexEmailRef{}).Error; err != nil {
		return err
	}
	if err := tx.db.Delete(&model.IndexEmail{}, "id = ?", emailID).Error; err != nil {
		return err
	}
	tx.record(TypeEmail, emailID, KindDestroyed)
	if len(mboxes) > 0 {
		var live []string
		if err := tx.db.Model(&model.IndexMailbox{}).Where("id IN ?", mboxes).Pluck("id", &live).Error; err != nil {
			return err
		}
		for _, m := range live {
			tx.record(TypeMailbox, m, KindCounts)
		}
	}
	var left int64
	if err := tx.db.Model(&model.IndexEmail{}).Where("thread_id = ?", e.ThreadID).Count(&left).Error; err != nil {
		return err
	}
	if left == 0 {
		tx.record(TypeThread, e.ThreadID, KindDestroyed)
	} else {
		tx.record(TypeThread, e.ThreadID, KindUpdated)
	}
	return nil
}

// SetKeywords stores an email's keywords (from IMAP flags) when they changed,
// and the membership's MODSEQ. A modseq lower than the stored one is stale data
// from a slower reader and is ignored (when both are known).
func (tx *Tx) SetKeywords(mailboxID string, uid uint32, kw Keywords, modseq uint64) error {
	var m Membership
	err := tx.db.Where("mailbox_id = ? AND uid = ?", mailboxID, uid).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if modseq != 0 && m.ModSeq != 0 && modseq < m.ModSeq {
		return nil
	}
	if modseq != m.ModSeq && modseq != 0 {
		if err := tx.db.Model(&Membership{}).Where("mailbox_id = ? AND uid = ?", mailboxID, uid).Update("mod_seq", modseq).Error; err != nil {
			return err
		}
	}
	var e model.IndexEmail
	if err := tx.db.Select("id", "keywords", "seen").Where("id = ?", m.EmailID).First(&e).Error; err != nil {
		return err
	}
	enc := kw.JSON()
	if enc == e.Keywords {
		return nil
	}
	err = tx.db.Model(&model.IndexEmail{}).Where("id = ?", e.ID).Updates(map[string]any{
		"keywords": enc, "seen": kw["$seen"], "flagged": kw["$flagged"], "draft": kw["$draft"], "answered": kw["$answered"],
	}).Error
	if err != nil {
		return err
	}
	tx.record(TypeEmail, e.ID, KindUpdated)
	if e.Seen != kw["$seen"] {
		tx.record(TypeMailbox, mailboxID, KindCounts)
	}
	return nil
}

func chunks[T any](s []T, n int) func(func([]T) bool) {
	return func(yield func([]T) bool) {
		for i := 0; i < len(s); i += n {
			if !yield(s[i:min(i+n, len(s))]) {
				return
			}
		}
	}
}

// UIDInfo is what a sync compare needs per UID.
type UIDInfo struct {
	EmailID  string
	ModSeq   uint64
	Keywords string
}

// FolderUIDs returns uid → (email, modseq, keywords) for a mailbox.
func (s *Store) FolderUIDs(mailboxID string) (map[uint32]UIDInfo, error) {
	type row struct {
		UID      uint32
		EmailID  string
		ModSeq   uint64
		Keywords string
	}
	var rows []row
	err := s.db.Table("memberships m").Select("m.uid, m.email_id, m.mod_seq, e.keywords").
		Joins("JOIN emails e ON e.id = m.email_id").Where("m.mailbox_id = ?", mailboxID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]UIDInfo, len(rows))
	for _, r := range rows {
		out[r.UID] = UIDInfo{EmailID: r.EmailID, ModSeq: r.ModSeq, Keywords: r.Keywords}
	}
	return out, nil
}
