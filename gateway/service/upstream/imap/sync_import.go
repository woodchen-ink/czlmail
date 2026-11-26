package imap

import (
	"context"
	"slices"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// orphanMatcher finds the orphan a newly seen message continues: the same stable
// server id, else the same (Message-ID, size, INTERNALDATE).
type orphanMatcher struct {
	byKey      map[string][]mailindex.Orphan
	byFallback map[string][]mailindex.Orphan
	used       map[string]bool
	left       int
}

func newOrphanMatcher(idx *mailindex.Store) (*orphanMatcher, error) {
	m := &orphanMatcher{byKey: map[string][]mailindex.Orphan{}, byFallback: map[string][]mailindex.Orphan{}, used: map[string]bool{}}
	_, err := idx.Write(func(tx *mailindex.Tx) error {
		list, err := tx.Orphans()
		for _, o := range list {
			m.add(o)
		}
		return err
	})
	return m, err
}

func (m *orphanMatcher) add(o mailindex.Orphan) {
	if o.UpstreamID != "" {
		m.byKey[o.UpstreamID] = append(m.byKey[o.UpstreamID], o)
	}
	m.byFallback[fallbackKey(o.MessageID, o.Size, o.ReceivedAt)] = append(m.byFallback[fallbackKey(o.MessageID, o.Size, o.ReceivedAt)], o)
	m.left++
}

// refresh adds orphans created after the matcher was built (a sweep detached more).
func (m *orphanMatcher) refresh(tx *mailindex.Tx) error {
	list, err := tx.Orphans()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, l := range m.byFallback {
		for _, o := range l {
			have[o.ID] = true
		}
	}
	for _, o := range list {
		if !have[o.ID] && !m.used[o.ID] {
			m.add(o)
		}
	}
	return nil
}

func (m *orphanMatcher) take(key, fallback string) (mailindex.Orphan, bool) {
	pick := func(list []mailindex.Orphan) (mailindex.Orphan, bool) {
		for _, o := range list {
			if !m.used[o.ID] {
				m.used[o.ID] = true
				m.left--
				return o, true
			}
		}
		return mailindex.Orphan{}, false
	}
	if key != "" {
		if o, ok := pick(m.byKey[key]); ok {
			return o, true
		}
		// A stable key that does not match must not fall back: it is another message.
		return mailindex.Orphan{}, false
	}
	return pick(m.byFallback[fallback])
}

func (m *orphanMatcher) unmatched() int {
	if m == nil {
		return 0
	}
	return m.left
}

// stableKey is the server's stable id for a message, "" when it has none.
func (u *Upstream) stableKey(m *imapc.Message) string {
	if f := u.opts.Dialect.StableKey; f != nil {
		if k := f(m); k != "" {
			return k
		}
	}
	switch {
	case m.EmailID != "":
		return "oid:" + m.EmailID
	case m.XGUID != "":
		return "guid:" + m.XGUID
	}
	return ""
}

func (u *Upstream) threadKey(m *imapc.Message) string {
	if f := u.opts.Dialect.ThreadKey; f != nil {
		if k := f(m); k != "" {
			return k
		}
	}
	if m.ThreadID != "" {
		return "oid:" + m.ThreadID
	}
	return ""
}

// metaItems are the FETCH items of a new message.
func (u *Upstream) metaItems() []string {
	caps := u.serverCaps()
	items := []string{"UID", "FLAGS", "INTERNALDATE", "RFC822.SIZE", "ENVELOPE", "BODYSTRUCTURE", "BODY.PEEK[HEADER.FIELDS (REFERENCES)]"}
	if caps.condstore {
		items = append(items, "MODSEQ")
	}
	if caps.objectID {
		items = append(items, "EMAILID", "THREADID")
	} else if caps.xguid {
		items = append(items, "X-GUID")
	}
	return append(items, u.opts.Dialect.FetchItems...)
}

// fetched is one new message with its preview.
type fetched struct {
	msg     *imapc.Message
	preview string
}

// fetchMeta fetches metadata and previews of uids in the selected folder.
func (u *Upstream) fetchMeta(ctx context.Context, c *imapc.Client, uids []uint32) ([]*fetched, error) {
	byUID := map[uint32]*fetched{}
	var order []*fetched
	err := c.Fetch(ctx, imapc.UIDSet(uids).Compact(), u.metaItems(), imapc.FetchOptions{UID: true}, func(m *imapc.Message) {
		if m.UID == 0 || byUID[m.UID] != nil {
			return
		}
		f := &fetched{msg: m}
		byUID[m.UID] = f
		order = append(order, f)
	}, nil)
	if err != nil {
		return nil, err
	}
	// One FETCH per distinct preview section ("1", "1.1", ...): usually a handful.
	sections := map[string][]uint32{}
	for _, f := range order {
		if f.msg.Body == nil {
			continue
		}
		if p := previewPart(f.msg.Body); p != nil {
			sections[p.Path] = append(sections[p.Path], f.msg.UID)
		}
	}
	keys := make([]string, 0, len(sections))
	for k := range sections {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, sec := range keys {
		item := "BODY.PEEK[" + sec + "]<0." + itoa(previewBytes) + ">"
		err := c.Fetch(ctx, imapc.UIDSet(sections[sec]).Compact(), []string{"UID", item}, imapc.FetchOptions{UID: true}, func(m *imapc.Message) {
			f := byUID[m.UID]
			if f == nil {
				return
			}
			if raw, ok := m.Sections[sec]; ok {
				if p := previewPart(f.msg.Body); p != nil {
					f.preview = makePreview(p, raw)
				}
			}
		}, nil)
		if err != nil {
			return nil, err
		}
	}
	return order, nil
}

// importFolder fetches the folder's new messages in batches, newest first, and
// commits each batch: a continued orphan is re-attached, anything else created.
func (r *round) importFolder(ctx context.Context, d *folderDelta) error {
	if len(d.newUIDs) == 0 {
		return nil
	}
	if sel := r.c.Selected(); sel == nil || sel.Name != d.mbox.Name {
		if _, err := r.c.Select(ctx, d.mbox.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
			return err
		}
	}
	for start := 0; start < len(d.newUIDs); start += importBatch {
		batch := d.newUIDs[start:min(start+importBatch, len(d.newUIDs))]
		msgs, err := r.u.fetchMeta(ctx, r.c, batch)
		if err != nil {
			return err
		}
		if err := r.write(func(tx *mailindex.Tx) error {
			for _, f := range msgs {
				if err := r.place(tx, d, f); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		r.done += len(batch)
		if r.req.Progress != nil && !r.u.idx.Ready() {
			r.req.Progress(min(r.done, r.total), r.total)
		}
	}
	return nil
}

func (r *round) place(tx *mailindex.Tx, d *folderDelta, f *fetched) error {
	m := f.msg
	has, err := tx.HasUID(d.mbox.ID, m.UID)
	if err != nil || has {
		return err // already imported by an interrupted round
	}
	key := r.u.stableKey(m)
	e, refs := toEmail(m, key, r.u.threadKey(m))
	e.Preview = f.preview
	if o, ok := r.matcher.take(key, fallbackKey(e.MessageID, e.Size, e.ReceivedAt)); ok {
		if err := tx.Attach(o, d.mbox.ID, m.UID, m.ModSeq); err != nil {
			return err
		}
		return tx.SetKeywords(d.mbox.ID, m.UID, mailindex.FlagsToKeywords(m.Flags), 0)
	}
	return tx.CreateEmail(e, refs, d.mbox.ID, m.UID, m.ModSeq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
