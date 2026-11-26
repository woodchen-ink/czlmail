package imap

import (
	"context"
	"errors"
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
	items := []string{"UID", "FLAGS", "INTERNALDATE", "RFC822.SIZE", "ENVELOPE", "BODY.PEEK[HEADER.FIELDS (REFERENCES)]"}
	if !caps.noBodyStructure {
		items = append(items, "BODYSTRUCTURE")
	}
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
	collect := func(m *imapc.Message) {
		if m.UID == 0 || byUID[m.UID] != nil {
			return
		}
		f := &fetched{msg: m}
		byUID[m.UID] = f
		order = append(order, f)
	}
	set := imapc.UIDSet(uids).Compact()
	err := c.Fetch(ctx, set, u.metaItems(), imapc.FetchOptions{UID: true}, collect, nil)
	var se *imapc.StatusError
	if errors.As(err, &se) && se.Status == "BAD" && !u.serverCaps().noBodyStructure {
		// A server without BODYSTRUCTURE: index without it (no attachment flag,
		// preview from the raw text).
		u.mu.Lock()
		u.caps.noBodyStructure = true
		u.mu.Unlock()
		u.log.Warn("server rejects BODYSTRUCTURE; indexing without it", "err", err)
		err = c.Fetch(ctx, set, u.metaItems(), imapc.FetchOptions{UID: true}, collect, nil)
	}
	if err != nil {
		return nil, err
	}
	// One FETCH per distinct preview section ("1", "1.1", ...): usually a handful.
	sections := map[string][]uint32{}
	for _, f := range order {
		if p := previewSource(f.msg); p != nil {
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
				if p := previewSource(f.msg); p != nil {
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

// previewSource is the part the preview is read from; without a body structure,
// the message text as plain text.
func previewSource(m *imapc.Message) *imapc.BodyPart {
	if m.Body == nil {
		return &imapc.BodyPart{Path: "TEXT", Type: "text", Subtype: "plain"}
	}
	return previewPart(m.Body)
}

// fetchMetaTolerant is fetchMeta that survives a NO for some messages (Gmail:
// "Some messages could not be FETCHed"): the batch is split until the failing
// UIDs are isolated and skipped, so one broken message cannot stall the sync.
// Skipped UIDs are tried again next round.
func (u *Upstream) fetchMetaTolerant(ctx context.Context, c *imapc.Client, uids []uint32) ([]*fetched, error) {
	msgs, err := u.fetchMeta(ctx, c, uids)
	var se *imapc.StatusError
	if err == nil || !errors.As(err, &se) || se.Status != "NO" {
		return msgs, err
	}
	if len(uids) == 1 {
		u.log.Warn("skipping a message the server cannot fetch", "uid", uids[0], "err", err)
		return nil, nil
	}
	half := len(uids) / 2
	a, err := u.fetchMetaTolerant(ctx, c, uids[:half])
	if err != nil {
		return nil, err
	}
	b, err := u.fetchMetaTolerant(ctx, c, uids[half:])
	return append(a, b...), err
}

// importFolder fetches the folder's new messages in batches, newest first, and
// commits each batch: a continued orphan is re-attached, anything else created.
func (r *round) importFolder(ctx context.Context, d *folderDelta) error {
	if len(d.newUIDs) == 0 {
		return nil
	}
	if sel := r.c.Selected(); sel == nil || sel.Name != d.mbox.Name {
		if _, err := r.c.Select(ctx, d.mbox.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
			if isOperational(err) {
				// The folder went away meanwhile: keep its old sync point, the next
				// LIST removes it.
				d.skip = true
				return nil
			}
			return err
		}
	}
	for start := 0; start < len(d.newUIDs); start += importBatch {
		batch := d.newUIDs[start:min(start+importBatch, len(d.newUIDs))]
		msgs, err := r.u.fetchMetaTolerant(ctx, r.c, batch)
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
	if err := tx.CreateEmail(e, refs, d.mbox.ID, m.UID, m.ModSeq); err != nil {
		return err
	}
	tx.Delivered(e.ID)
	return nil
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
