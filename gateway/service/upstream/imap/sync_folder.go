package imap

import (
	"context"
	"slices"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// recentWindow is how many of the newest messages a server without CONDSTORE has
// its flags compared on every check; older ones are compared nightly (Deep).
const recentWindow = 2000

// folderDelta is what changed in one folder since its sync point.
type folderDelta struct {
	mbox     *model.IndexMailbox
	known    map[uint32]mailindex.UIDInfo
	reset    bool     // UIDVALIDITY changed: every old UID is gone
	vanished []uint32 // known UIDs no longer there
	flags    []flagChange
	newUIDs  []uint32 // not indexed yet, newest first
	next     syncPoint
	removed  bool // applyRemovals committed
	skip     bool // the folder could not be selected for import: keep its sync point
	imported bool
	relabel  bool // Gmail: every message's labels were fetched
}

type flagChange struct {
	uid    uint32
	kw     mailindex.Keywords
	modseq uint64
	// labels are Gmail's X-GM-LABELS, when fetched (hasLabels).
	labels    []string
	hasLabels bool
}

type syncPoint struct {
	UIDValidity   uint32
	UIDNext       uint32
	HighestModSeq uint64
	Messages      uint32
}

func (d *folderDelta) changed() bool {
	return d.reset || len(d.vanished) > 0 || len(d.flags) > 0 || len(d.newUIDs) > 0
}

func (d *folderDelta) applyRemovals(tx *mailindex.Tx) error {
	if d.reset {
		if err := tx.DetachAll(d.mbox.ID); err != nil {
			return err
		}
	} else if len(d.vanished) > 0 {
		if err := tx.Detach(d.mbox.ID, d.vanished); err != nil {
			return err
		}
	}
	for _, f := range d.flags {
		if err := tx.SetKeywords(d.mbox.ID, f.uid, f.kw, f.modseq); err != nil {
			return err
		}
	}
	return nil
}

// noteFetched sorts one FETCH (UID FLAGS [MODSEQ]) into flag changes and new UIDs.
func (d *folderDelta) noteFetched(m *imapc.Message, isNew map[uint32]bool) {
	if m.UID == 0 {
		return
	}
	info, ok := d.known[m.UID]
	if !ok {
		if !isNew[m.UID] {
			isNew[m.UID] = true
			d.newUIDs = append(d.newUIDs, m.UID)
		}
		return
	}
	if !m.HasFlags {
		return
	}
	kw := mailindex.FlagsToKeywords(m.Flags)
	labels, hasLabels := gmLabels(m)
	// A label change is invisible in FLAGS: with labels, every fetched message
	// is compared (setting the same labels again is a no-op).
	if hasLabels || kw.JSON() != info.Keywords || m.ModSeq != 0 && m.ModSeq != info.ModSeq {
		d.flags = append(d.flags, flagChange{uid: m.UID, kw: kw, modseq: m.ModSeq, labels: labels, hasLabels: hasLabels})
	}
}

func pointOf(mb *imapc.Mailbox) syncPoint {
	return syncPoint{UIDValidity: mb.UIDValidity, UIDNext: mb.UIDNext, HighestModSeq: mb.HighestModSeq, Messages: mb.Exists}
}

// folderDelta selects the folder (read-only) and computes its delta with the
// best tier the server offers.
func (r *round) folderDelta(ctx context.Context, m *model.IndexMailbox, st *imapc.Status) (*folderDelta, error) {
	known, err := r.u.idx.FolderUIDs(m.ID)
	if err != nil {
		return nil, err
	}
	d := &folderDelta{mbox: m, known: known}
	isNew := map[uint32]bool{}

	if r.caps.qresync && m.UIDValidity != 0 && m.HighestModSeq != 0 {
		mb, err := r.c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true, QResync: &imapc.QResync{UIDValidity: m.UIDValidity, ModSeq: m.HighestModSeq}})
		if err != nil {
			return nil, err
		}
		d.next = pointOf(mb)
		if mb.UIDValidity != m.UIDValidity {
			return d, r.resetDelta(ctx, d)
		}
		if !mb.NoModSeq {
			gone := map[uint32]bool{}
			for _, uid := range mb.Vanished {
				if _, ok := known[uid]; ok && !gone[uid] {
					gone[uid] = true
					d.vanished = append(d.vanished, uid)
				}
			}
			for _, msg := range mb.Changed {
				d.noteFetched(msg, isNew)
			}
			if int(mb.Exists) != len(known)-len(d.vanished)+len(d.newUIDs) {
				return d, r.compareAll(ctx, d, isNew)
			}
			sortDesc(d.newUIDs)
			return d, nil
		}
	}

	mb, err := r.c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true, CondStore: r.caps.condstore})
	if err != nil {
		return nil, err
	}
	d.next = pointOf(mb)
	if m.UIDValidity == 0 || mb.UIDValidity != m.UIDValidity {
		return d, r.resetDelta(ctx, d)
	}
	if r.caps.condstore && !mb.NoModSeq && m.HighestModSeq != 0 {
		since := m.HighestModSeq
		if r.u.relabelling(m) {
			d.relabel, since = true, 0 // every message, not only the changed ones
		}
		if mb.HighestModSeq != m.HighestModSeq || d.relabel {
			err := r.c.Fetch(ctx, "1:*", r.u.flagItems("UID", "FLAGS", "MODSEQ"),
				imapc.FetchOptions{UID: true, ChangedSince: since},
				func(msg *imapc.Message) { d.noteFetched(msg, isNew) }, nil)
			if err != nil {
				return nil, err
			}
		}
		if int(mb.Exists) != len(known)+len(d.newUIDs) {
			return d, r.compareAll(ctx, d, isNew)
		}
		sortDesc(d.newUIDs)
		return d, nil
	}
	return d, r.compareFlags(ctx, d, mb, isNew)
}

// resetDelta handles a new folder or a changed UIDVALIDITY: every current UID is
// new, and every indexed one is detached so it can be matched again.
func (r *round) resetDelta(ctx context.Context, d *folderDelta) error {
	d.reset = d.mbox.UIDValidity != 0 || len(d.known) > 0
	all, err := r.c.SearchUIDs(ctx, imapc.Atom("ALL"))
	if err != nil {
		return err
	}
	d.newUIDs = append([]uint32(nil), all...)
	sortDesc(d.newUIDs)
	return nil
}

// compareAll lists every UID to find deletions the counts revealed.
func (r *round) compareAll(ctx context.Context, d *folderDelta, isNew map[uint32]bool) error {
	all, err := r.c.SearchUIDs(ctx, imapc.Atom("ALL"))
	if err != nil {
		return err
	}
	present := make(map[uint32]bool, len(all))
	for _, uid := range all {
		present[uid] = true
		if _, ok := d.known[uid]; !ok && !isNew[uid] {
			isNew[uid] = true
			d.newUIDs = append(d.newUIDs, uid)
		}
	}
	d.vanished = d.vanished[:0]
	for uid := range d.known {
		if !present[uid] {
			d.vanished = append(d.vanished, uid)
		}
	}
	// A UID listed as new that is no longer present was added and removed meanwhile.
	d.newUIDs = slices.DeleteFunc(d.newUIDs, func(u uint32) bool { return !present[u] })
	sortDesc(d.newUIDs)
	return nil
}

// compareFlags is the tier without CONDSTORE: the newest recentWindow messages
// have UIDs and flags compared; older ones only when their count is off, or
// everything on a Deep round.
func (r *round) compareFlags(ctx context.Context, d *folderDelta, mb *imapc.Mailbox, isNew map[uint32]bool) error {
	if mb.Exists == 0 {
		for uid := range d.known {
			d.vanished = append(d.vanished, uid)
		}
		return nil
	}
	lo := uint32(1)
	whole := r.req.Deep || mb.Exists <= recentWindow
	if !whole {
		lo = mb.Exists - recentWindow + 1
	}
	present := map[uint32]bool{}
	minUID := uint32(0)
	err := r.c.Fetch(ctx, imapc.Range(lo, 0), r.u.flagItems("UID", "FLAGS"), imapc.FetchOptions{},
		func(msg *imapc.Message) {
			if msg.UID == 0 {
				return
			}
			present[msg.UID] = true
			if minUID == 0 || msg.UID < minUID {
				minUID = msg.UID
			}
			d.noteFetched(msg, isNew)
		}, nil)
	if err != nil {
		return err
	}
	older := 0
	for uid := range d.known {
		switch {
		case whole || uid >= minUID:
			if !present[uid] {
				d.vanished = append(d.vanished, uid)
			}
		default:
			older++
		}
	}
	if !whole && older != int(mb.Exists)-len(present) {
		d.vanished = nil
		return r.compareAll(ctx, d, isNew)
	}
	sortDesc(d.newUIDs)
	return nil
}

func sortDesc(s []uint32) {
	slices.SortFunc(s, func(a, b uint32) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		}
		return 0
	})
}
