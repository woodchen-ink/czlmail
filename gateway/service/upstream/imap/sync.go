package imap

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// importBatch is how many new messages are fetched and committed together.
const importBatch = 500

// Sync runs one round; see upstream.Upstream.
func (u *Upstream) Sync(ctx context.Context, req upstream.SyncRequest) (map[string]string, error) {
	u.op.Lock()
	defer u.op.Unlock()
	r := &round{u: u, req: req, now: u.opts.Now(), states: map[string]string{}}
	err := u.withConn(ctx, func(c *imapc.Client) error {
		r.c = c
		return r.run(ctx)
	})
	if len(r.states) == 0 {
		return nil, err
	}
	return r.states, err
}

// round is one sync round on one connection.
type round struct {
	u      *Upstream
	c      *imapc.Client
	req    upstream.SyncRequest
	now    time.Time
	caps   serverCaps
	states map[string]string

	byName   map[string]*model.IndexMailbox
	statuses map[string]*imapc.Status
	listed   bool
	checked  map[string]bool // mailbox ids compared this round
	deltas   []*folderDelta
	matcher  *orphanMatcher

	done, total int
}

func (r *round) write(fn func(*mailindex.Tx) error) error {
	st, err := r.u.idx.Write(fn)
	maps.Copy(r.states, st)
	return err
}

func (r *round) run(ctx context.Context) error {
	r.caps = r.u.serverCaps()
	ready := r.u.idx.Ready()
	full := r.req.Full || r.req.Deep || !ready
	r.checked = map[string]bool{}
	r.statuses = map[string]*imapc.Status{}

	// STATUS (and LIST-STATUS) of the selected mailbox reports the session's own,
	// possibly stale view (RFC 3501 §6.3.10); a pooled connection may still have one.
	if err := r.c.Unselect(ctx); err != nil && !isOperational(err) {
		return err
	}
	hinted := map[string]bool{}
	for _, n := range r.req.Mailboxes {
		hinted[n] = true
	}
	if full || r.u.listDue(r.now) {
		if err := r.list(ctx); err != nil {
			return err
		}
	} else if err := r.loadMailboxes(); err != nil {
		return err
	}

	var candidates []*model.IndexMailbox
	for _, m := range r.byName {
		if m.NoSelect || m.Virtual {
			continue
		}
		if full || hinted[m.Name] || r.u.isDue(m.ID, r.now) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if err := r.examine(ctx, candidates, hinted, false); err != nil {
		return err
	}
	// A message that appeared or vanished here may have moved from or to a folder
	// this round did not look at. Check the others by STATUS before importing, so
	// the move is matched (same id) instead of showing up as a new message.
	if !full && r.anyChanged() {
		if err := r.sweep(ctx); err != nil {
			return err
		}
	}
	if err := r.apply(ctx); err != nil {
		return err
	}
	return r.finish(ready)
}

func (r *round) anyChanged() bool {
	for _, d := range r.deltas {
		if len(d.newUIDs) > 0 || len(d.vanished) > 0 || d.reset {
			return true
		}
	}
	return false
}

// list runs LIST (with STATUS when the server has LIST-STATUS) and reconciles folders.
func (r *round) list(ctx context.Context) error {
	entries, err := r.c.List(ctx, true)
	if err != nil {
		return err
	}
	r.listed = true
	if err := r.reconcileMailboxes(ctx, entries); err != nil {
		return err
	}
	r.u.listed(r.now, r.req.Idle)
	return r.loadMailboxes()
}

func (r *round) loadMailboxes() error {
	var rows []model.IndexMailbox
	if err := r.u.idx.DB().Find(&rows).Error; err != nil {
		return err
	}
	r.byName = make(map[string]*model.IndexMailbox, len(rows))
	for i := range rows {
		r.byName[rows[i].Name] = &rows[i]
	}
	return nil
}

// examine decides which candidates changed and computes their deltas.
// In a sweep, STATUS is always asked and a folder counts as changed only when its
// counts or UIDs moved (the flag-window compare of the plain tier waits for its turn).
func (r *round) examine(ctx context.Context, candidates []*model.IndexMailbox, hinted map[string]bool, sweep bool) error {
	slices.SortFunc(candidates, func(a, b *model.IndexMailbox) int {
		if d := a.SortOrder - b.SortOrder; d != 0 {
			return d
		}
		return strings.Compare(a.Name, b.Name)
	})
	for _, m := range candidates {
		if r.checked[m.ID] {
			continue
		}
		st := r.statuses[m.Name]
		if st == nil && (!r.caps.qresync || sweep) {
			var err error
			if st, err = r.c.Status(ctx, m.Name); err != nil {
				if isOperational(err) {
					continue // the folder vanished between LIST and STATUS
				}
				return err
			}
			r.statuses[m.Name] = st
		}
		r.checked[m.ID] = true
		if !r.needsSync(m, st, hinted[m.Name] || r.req.Deep, sweep) {
			r.u.checked(m.ID, m.Role, false, r.req.Idle, r.now)
			continue
		}
		d, err := r.folderDelta(ctx, m, st)
		if err != nil {
			if isOperational(err) {
				continue
			}
			return err
		}
		r.deltas = append(r.deltas, d)
	}
	return nil
}

// needsSync compares STATUS with the stored sync point. Without CONDSTORE a flag
// change is invisible to STATUS, so due folders are always compared (window).
func (r *round) needsSync(m *model.IndexMailbox, st *imapc.Status, forced, sweep bool) bool {
	if st == nil || m.UIDValidity == 0 || forced || r.u.relabelling(m) {
		return true
	}
	if st.UIDValidity != m.UIDValidity || st.UIDNext != m.UIDNext || st.Messages != m.Messages {
		return true
	}
	if sweep && !(r.caps.condstore && st.HasModSeq) {
		return false
	}
	if r.caps.condstore && st.HasModSeq {
		return st.HighestModSeq != m.HighestModSeq
	}
	return true
}

// apply commits detachments and flag changes, then imports new messages.
func (r *round) apply(ctx context.Context) error {
	if err := r.write(func(tx *mailindex.Tx) error {
		for _, d := range r.deltas {
			if d.removed {
				continue // applied before a sweep; a second DetachAll would undo the import
			}
			if err := d.applyRemovals(tx); err != nil {
				return err
			}
			if err := r.u.applyFlagLabels(tx, d); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, d := range r.deltas {
		d.removed = true
	}
	if r.matcher == nil {
		m, err := newOrphanMatcher(r.u.idx)
		if err != nil {
			return err
		}
		r.matcher = m
	} else if err := r.write(r.matcher.refresh); err != nil {
		return err
	}
	r.total = 0
	for _, d := range r.deltas {
		r.total += len(d.newUIDs)
	}
	for _, d := range r.deltas {
		if d.imported {
			continue
		}
		if err := r.importFolder(ctx, d); err != nil {
			return err
		}
		d.imported = true
	}
	return nil
}

// sweep compares the folders not looked at yet by STATUS (their message counts
// change with a move); only those that changed get a delta.
func (r *round) sweep(ctx context.Context) error {
	if !r.listed {
		if err := r.list(ctx); err != nil {
			return err
		}
	}
	var rest []*model.IndexMailbox
	for _, m := range r.byName {
		if !m.NoSelect && !m.Virtual && !r.checked[m.ID] {
			rest = append(rest, m)
		}
	}
	return r.examine(ctx, rest, nil, true)
}

// finish destroys what never turned up again, commits the folders' sync points,
// and marks the index ready after the first complete round.
func (r *round) finish(wasReady bool) error {
	now := r.now.UTC()
	err := r.write(func(tx *mailindex.Tx) error {
		if _, err := tx.DestroyOrphans(); err != nil {
			return err
		}
		for _, d := range r.deltas {
			if d.skip {
				continue
			}
			m := d.mbox
			point := map[string]any{
				"uid_validity": d.next.UIDValidity, "uid_next": d.next.UIDNext,
				"highest_mod_seq": d.next.HighestModSeq, "messages": d.next.Messages, "synced_at": now,
			}
			if st := r.statuses[m.Name]; st != nil {
				point["unseen"] = st.Unseen
			}
			if d.changed() {
				point["changed_at"] = now
			}
			// Only the sync point: LIST may have updated other columns this round.
			if err := tx.DB().Model(&model.IndexMailbox{}).Where("id = ?", m.ID).Updates(point).Error; err != nil {
				return err
			}
		}
		if !wasReady {
			tx.SetReady()
		}
		if r.req.Deep {
			tx.SetLastFullSync(now)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range r.deltas {
		r.u.checked(d.mbox.ID, d.mbox.Role, d.changed(), r.req.Idle, r.now)
		if d.relabel {
			r.u.gm.relabel.Store(false)
		}
	}
	return nil
}

// reconcileMailboxes brings the mailbox rows in line with LIST: new folders are
// created, gone ones removed (their messages orphaned, so a message moved out of a
// deleted folder keeps its id), renames keep the id when MAILBOXID or UIDVALIDITY
// shows it is the same folder.
func (r *round) reconcileMailboxes(ctx context.Context, entries []*imapc.ListEntry) error {
	var existing []model.IndexMailbox
	if err := r.u.idx.DB().Find(&existing).Error; err != nil {
		return err
	}
	// Gmail: only All Mail, Trash and Spam are synced folders; the other
	// folders are labels, reconciled as virtual mailboxes below.
	all := entries
	var kinds []gmailEntry
	allMailName := ""
	if r.u.gm != nil {
		kinds = make([]gmailEntry, len(entries))
		entries = nil
		for i, e := range all {
			k, ok := classifyGmail(e)
			if !ok {
				k = gmailEntry{}
			}
			kinds[i] = k
			if k.sync {
				entries = append(entries, e)
				if k.hidden {
					allMailName = e.Name
				}
			}
		}
		existing = slices.DeleteFunc(existing, func(m model.IndexMailbox) bool { return m.Virtual })
	}
	byName := map[string]*model.IndexMailbox{}
	byUpstream := map[string]*model.IndexMailbox{}
	for i := range existing {
		m := &existing[i]
		byName[m.Name] = m
		if m.UpstreamID != "" {
			byUpstream[m.UpstreamID] = m
		}
	}
	seen := map[string]bool{}
	type pending struct {
		e   *imapc.ListEntry
		row *model.IndexMailbox
		new bool
	}
	var list []pending
	for _, e := range entries {
		if r.u.opts.Dialect.SkipMailbox != nil && r.u.opts.Dialect.SkipMailbox(e) {
			continue
		}
		if e.Status != nil {
			r.statuses[e.Name] = e.Status
		}
		row := byName[e.Name]
		if row == nil && e.Status != nil && e.Status.MailboxID != "" {
			row = byUpstream[e.Status.MailboxID]
		}
		if row != nil {
			seen[row.ID] = true
			list = append(list, pending{e: e, row: row})
			continue
		}
		list = append(list, pending{e: e, new: true})
	}
	var gone []*model.IndexMailbox
	for i := range existing {
		if !seen[existing[i].ID] {
			gone = append(gone, &existing[i])
		}
	}
	// Rename without MAILBOXID: a gone folder and a new one with the same UIDVALIDITY.
	for i := range list {
		p := &list[i]
		if !p.new || len(gone) == 0 || noSelect(p.e) {
			continue
		}
		st := r.statuses[p.e.Name]
		if st == nil {
			s, err := r.c.Status(ctx, p.e.Name)
			if err != nil {
				if isOperational(err) {
					continue
				}
				return err
			}
			st, r.statuses[p.e.Name] = s, s
		}
		for j, g := range gone {
			if g.UIDValidity != 0 && g.UIDValidity == st.UIDValidity {
				p.row, p.new = g, false
				gone = slices.Delete(gone, j, j+1)
				break
			}
		}
	}
	return r.write(func(tx *mailindex.Tx) error {
		ids := map[string]string{} // name → id, for parents
		for i := range list {
			p := &list[i]
			if p.new {
				p.row = &model.IndexMailbox{ID: mailindex.NewMailboxID(), Subscribed: true}
			}
			ids[p.e.Name] = p.row.ID
		}
		for _, g := range gone {
			if err := tx.RemoveMailbox(g.ID); err != nil {
				return err
			}
			r.u.forget(g.ID)
		}
		for i := range list {
			p := &list[i]
			before := *p.row
			m := p.row
			role := roleOf(p.e)
			m.Name, m.Delimiter, m.Role, m.NoSelect = p.e.Name, p.e.Delimiter, role, noSelect(p.e)
			if r.u.gm != nil {
				m.Hidden = role == "all"
			}
			m.ParentID = ids[parentName(p.e.Name, p.e.Delimiter)]
			m.SortOrder = sortOrder(role)
			attrs, _ := json.Marshal(p.e.Attrs)
			m.Attrs = string(attrs)
			if st := r.statuses[p.e.Name]; st != nil && st.MailboxID != "" {
				m.UpstreamID = st.MailboxID
			}
			if p.new {
				if err := tx.CreateMailbox(m); err != nil {
					return err
				}
				continue
			}
			visible := before.Name != m.Name || before.ParentID != m.ParentID || before.Role != m.Role || before.NoSelect != m.NoSelect || before.Hidden != m.Hidden
			if visible || before.Attrs != m.Attrs || before.Delimiter != m.Delimiter || before.UpstreamID != m.UpstreamID || before.SortOrder != m.SortOrder {
				if err := tx.UpdateMailbox(m, visible); err != nil {
					return err
				}
			}
		}
		if r.u.gm != nil {
			return r.u.reconcileLabels(tx, all, kinds, allMailName)
		}
		return nil
	})
}
