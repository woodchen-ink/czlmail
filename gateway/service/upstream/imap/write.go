package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// ErrNotFound means an email or mailbox id is not in the index.
var ErrNotFound = upstream.ErrNotFound

func rejected(op string, err error) error {
	return &upstream.Error{Kind: upstream.KindRejected, Op: op, Err: err}
}

type placed struct {
	emailID string
	uid     uint32
}

// locate groups emails by mailbox.
func (u *Upstream) locate(emailIDs []string) (map[string][]placed, error) {
	var rows []model.IndexMembership
	if err := u.idx.DB().Where("email_id IN ?", emailIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string][]placed{}
	for _, r := range rows {
		out[r.MailboxID] = append(out[r.MailboxID], placed{emailID: r.EmailID, uid: r.UID})
	}
	return out, nil
}

func (u *Upstream) mailbox(id string) (*model.IndexMailbox, error) {
	var m model.IndexMailbox
	err := u.idx.DB().First(&m, "id = ?", id).Error
	if err != nil {
		return nil, rejected("mailbox", fmt.Errorf("%w: mailbox %s", ErrNotFound, id))
	}
	return &m, nil
}

func uidsOf(list []placed) imapc.UIDSet {
	out := make(imapc.UIDSet, len(list))
	for i, p := range list {
		out[i] = p.uid
	}
	return out
}

// errStaleUIDs means the folder's UIDVALIDITY changed since the index was synced:
// the stored UIDs may now name other messages, so nothing is written.
var errStaleUIDs = errors.New("folder was rebuilt on the server; sync first")

// selectRW selects m for writing (reusing the current selection) and checks that
// its UIDVALIDITY is still the indexed one.
func selectRW(ctx context.Context, c *imapc.Client, m *model.IndexMailbox) error {
	sel := c.Selected()
	if sel == nil || sel.Name != m.Name || sel.ReadOnly {
		var err error
		if sel, err = c.Select(ctx, m.Name, imapc.SelectOptions{}); err != nil {
			return err
		}
	}
	if m.UIDValidity != 0 && sel.UIDValidity != m.UIDValidity {
		return &upstream.Error{Kind: upstream.KindUnreachable, Op: "select", Err: errStaleUIDs}
	}
	return nil
}

// writeOp runs a write under the op lock on a work connection.
func (u *Upstream) writeOp(ctx context.Context, fn func(c *imapc.Client, states map[string]string) error) (map[string]string, error) {
	u.op.Lock()
	defer u.op.Unlock()
	states := map[string]string{}
	err := u.withConn(ctx, func(c *imapc.Client) error { return fn(c, states) })
	return states, err
}

func (u *Upstream) commit(states map[string]string, fn func(*mailindex.Tx) error) error {
	st, err := u.idx.Write(fn)
	maps.Copy(states, st)
	return err
}

// SetKeywords adds and removes keywords on emails.
func (u *Upstream) SetKeywords(ctx context.Context, emailIDs []string, add, remove []string) (map[string]string, error) {
	return u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		where, err := u.locate(emailIDs)
		if err != nil {
			return err
		}
		for mboxID, list := range where {
			m, err := u.mailbox(mboxID)
			if err != nil {
				return err
			}
			if err := selectRW(ctx, c, m); err != nil {
				return err
			}
			modseq := map[uint32]uint64{}
			onFetch := func(msg *imapc.Message) {
				if msg.UID != 0 {
					modseq[msg.UID] = msg.ModSeq
				}
			}
			uids := uidsOf(list)
			if len(add) > 0 {
				if err := c.Store(ctx, uids, imapc.StoreAdd, flagsOf(add), onFetch); err != nil {
					return err
				}
			}
			if len(remove) > 0 {
				if err := c.Store(ctx, uids, imapc.StoreRemove, flagsOf(remove), onFetch); err != nil {
					return err
				}
			}
			infos, err := u.idx.FolderUIDs(mboxID)
			if err != nil {
				return err
			}
			if err := u.commit(states, func(tx *mailindex.Tx) error {
				for _, p := range list {
					kw := mailindex.ParseKeywords(infos[p.uid].Keywords)
					for _, k := range add {
						kw[strings.ToLower(k)] = true
					}
					for _, k := range remove {
						delete(kw, strings.ToLower(k))
					}
					if err := tx.SetKeywords(mboxID, p.uid, kw, modseq[p.uid]); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func flagsOf(keywords []string) []string {
	out := make([]string, len(keywords))
	for i, k := range keywords {
		out[i] = mailindex.KeywordToFlag(k)
	}
	return out
}

// Move moves emails to a mailbox; they keep their ids.
func (u *Upstream) Move(ctx context.Context, emailIDs []string, toMailboxID string) (map[string]string, error) {
	return u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		where, err := u.locate(emailIDs)
		if err != nil {
			return err
		}
		dest, err := u.mailbox(toMailboxID)
		if err != nil {
			return err
		}
		for mboxID, list := range where {
			if mboxID == toMailboxID {
				continue
			}
			src, err := u.mailbox(mboxID)
			if err != nil {
				return err
			}
			if err := selectRW(ctx, c, src); err != nil {
				return err
			}
			uids := uidsOf(list)
			var cu *imapc.CopyUID
			if u.serverCaps().move {
				cu, err = c.Move(ctx, uids, dest.Name)
			} else {
				cu, err = c.Copy(ctx, uids, dest.Name)
				if err == nil {
					err = c.Store(ctx, uids, imapc.StoreAdd, []string{`\Deleted`}, nil)
				}
				if err == nil {
					err = c.UIDExpunge(ctx, uids)
				}
			}
			if err != nil {
				return err
			}
			if err := u.rebindOrResync(ctx, c, states, list, src, dest, cu); err != nil {
				return err
			}
		}
		return nil
	})
}

// rebindOrResync records a move. With COPYUID every email is rebound to its new
// UID; without it the source loses the UIDs and the destination is synced, which
// matches the moved messages to their old ids.
func (u *Upstream) rebindOrResync(ctx context.Context, c *imapc.Client, states map[string]string, list []placed, src, dest *model.IndexMailbox, cu *imapc.CopyUID) error {
	if cu != nil && cu.UIDValidity == dest.UIDValidity {
		mapping := cu.Map()
		return u.commit(states, func(tx *mailindex.Tx) error {
			for _, p := range list {
				if to, ok := mapping[p.uid]; ok {
					if err := tx.Rebind(p.emailID, src.ID, p.uid, dest.ID, to); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	if err := u.commit(states, func(tx *mailindex.Tx) error { return tx.Detach(src.ID, uidsOf(list)) }); err != nil {
		return err
	}
	return u.resync(ctx, c, states, dest.Name, src.Name)
}

// resync runs a sync round for some folders inside a write (the op lock is held).
func (u *Upstream) resync(ctx context.Context, c *imapc.Client, states map[string]string, names ...string) error {
	r := &round{u: u, c: c, req: upstream.SyncRequest{Mailboxes: names}, now: u.opts.Now(), states: states}
	return r.run(ctx)
}

// Copy copies emails; the copies are new emails.
func (u *Upstream) Copy(ctx context.Context, emailIDs []string, toMailboxID string) ([]string, map[string]string, error) {
	created := map[string]string{} // source email → copy
	states, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		where, err := u.locate(emailIDs)
		if err != nil {
			return err
		}
		dest, err := u.mailbox(toMailboxID)
		if err != nil {
			return err
		}
		for mboxID, list := range where {
			src, err := u.mailbox(mboxID)
			if err != nil {
				return err
			}
			if err := selectRW(ctx, c, src); err != nil {
				return err
			}
			cu, err := c.Copy(ctx, uidsOf(list), dest.Name)
			if err != nil {
				return err
			}
			if cu == nil {
				if err := u.resync(ctx, c, states, dest.Name); err != nil {
					return err
				}
				continue
			}
			mapping := cu.Map()
			var newUIDs []uint32
			back := map[uint32]string{}
			for _, p := range list {
				if to, ok := mapping[p.uid]; ok {
					newUIDs = append(newUIDs, to)
					back[to] = p.emailID
				}
			}
			ids, err := u.importUIDs(ctx, c, states, dest, newUIDs)
			if err != nil {
				return err
			}
			for uid, id := range ids {
				created[back[uid]] = id
			}
		}
		return nil
	})
	out := make([]string, len(emailIDs))
	for i, id := range emailIDs {
		out[i] = created[id]
	}
	return out, states, err
}

// importUIDs fetches and creates specific new messages of a folder (APPEND / COPY
// results), returning uid → new email id.
func (u *Upstream) importUIDs(ctx context.Context, c *imapc.Client, states map[string]string, m *model.IndexMailbox, uids []uint32) (map[uint32]string, error) {
	out := map[uint32]string{}
	if len(uids) == 0 {
		return out, nil
	}
	if _, err := c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
		return nil, err
	}
	msgs, err := u.fetchMeta(ctx, c, uids)
	if err != nil {
		return nil, err
	}
	err = u.commit(states, func(tx *mailindex.Tx) error {
		for _, f := range msgs {
			has, err := tx.HasUID(m.ID, f.msg.UID)
			if err != nil {
				return err
			}
			if has {
				continue
			}
			e, refs := toEmail(f.msg, u.stableKey(f.msg), u.threadKey(f.msg))
			e.Preview = f.preview
			if err := tx.CreateEmail(e, refs, m.ID, f.msg.UID, f.msg.ModSeq); err != nil {
				return err
			}
			out[f.msg.UID] = e.ID
		}
		return nil
	})
	return out, err
}

// Append stores a new message and returns its email id.
func (u *Upstream) Append(ctx context.Context, mailboxID string, raw []byte, keywords []string, receivedAt time.Time) (string, map[string]string, error) {
	m, err := u.mailbox(mailboxID)
	if err != nil {
		return "", nil, err
	}
	var id string
	states, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		au, err := c.Append(ctx, m.Name, flagsOf(keywords), receivedAt, raw)
		if err != nil {
			return err
		}
		if au != nil && (m.UIDValidity == 0 || au.UIDValidity == m.UIDValidity) {
			ids, err := u.importUIDs(ctx, c, states, m, []uint32{au.UID})
			id = ids[au.UID]
			return err
		}
		// No APPENDUID: sync the folder, then find the message by its Message-ID.
		if err := u.resync(ctx, c, states, m.Name); err != nil {
			return err
		}
		msgID := firstID(headerValue(raw, "Message-Id"))
		if msgID == "" {
			return nil
		}
		var e model.IndexEmail
		err = u.idx.DB().Table("emails").Select("emails.id").Joins("JOIN memberships ms ON ms.email_id = emails.id").
			Where("ms.mailbox_id = ? AND emails.message_id = ?", m.ID, msgID).Order("ms.uid DESC").Limit(1).Scan(&e).Error
		id = e.ID
		return err
	})
	return id, states, err
}

func headerValue(raw []byte, name string) string {
	head := string(raw)
	if i := strings.Index(head, "\r\n\r\n"); i >= 0 {
		head = head[:i]
	} else if i := strings.Index(head, "\n\n"); i >= 0 {
		head = head[:i]
	}
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(head, "\n") {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// Destroy permanently removes emails (\Deleted + UID EXPUNGE).
func (u *Upstream) Destroy(ctx context.Context, emailIDs []string) (map[string]string, error) {
	return u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		where, err := u.locate(emailIDs)
		if err != nil {
			return err
		}
		for mboxID, list := range where {
			m, err := u.mailbox(mboxID)
			if err != nil {
				return err
			}
			if err := selectRW(ctx, c, m); err != nil {
				return err
			}
			uids := uidsOf(list)
			if err := c.Store(ctx, uids, imapc.StoreAdd, []string{`\Deleted`}, nil); err != nil {
				return err
			}
			if err := c.UIDExpunge(ctx, uids); err != nil {
				return err
			}
			if err := u.commit(states, func(tx *mailindex.Tx) error { return tx.RemoveUIDs(mboxID, uids) }); err != nil {
				return err
			}
			if !u.serverCaps().uidplus {
				// Plain EXPUNGE removed every \Deleted message of the folder.
				if err := u.resync(ctx, c, states, m.Name); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// CreateMailbox creates a folder under parentID ("" for the top level).
func (u *Upstream) CreateMailbox(ctx context.Context, parentID, name string) (string, map[string]string, error) {
	var id string
	states, err := u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		full, delim, err := u.childName(ctx, c, parentID, name)
		if err != nil {
			return err
		}
		if err := c.Create(ctx, full); err != nil {
			return err
		}
		_ = c.Subscribe(ctx, full)
		st, err := c.Status(ctx, full)
		if err != nil {
			return err
		}
		m := &model.IndexMailbox{Name: full, Delimiter: delim, ParentID: parentID, Subscribed: true, SortOrder: sortOrder(""),
			UIDValidity: st.UIDValidity, UIDNext: st.UIDNext, HighestModSeq: st.HighestModSeq, Messages: st.Messages, UpstreamID: st.MailboxID, Attrs: "[]"}
		if err := u.commit(states, func(tx *mailindex.Tx) error { return tx.CreateMailbox(m) }); err != nil {
			return err
		}
		id = m.ID
		return nil
	})
	return id, states, err
}

// childName builds the full IMAP name of name under parentID.
func (u *Upstream) childName(ctx context.Context, c *imapc.Client, parentID, name string) (string, string, error) {
	if parentID == "" {
		delim, err := u.delimiter(ctx, c)
		if err != nil {
			return "", "", err
		}
		if delim != "" && strings.Contains(name, delim) {
			return "", "", rejected("name", fmt.Errorf("name contains the hierarchy delimiter %q", delim))
		}
		return name, delim, nil
	}
	p, err := u.mailbox(parentID)
	if err != nil {
		return "", "", err
	}
	if p.Delimiter == "" {
		return "", "", rejected("name", errors.New("the server has no folder hierarchy"))
	}
	if strings.Contains(name, p.Delimiter) {
		return "", "", rejected("name", fmt.Errorf("name contains the hierarchy delimiter %q", p.Delimiter))
	}
	return p.Name + p.Delimiter + name, p.Delimiter, nil
}

func (u *Upstream) delimiter(ctx context.Context, c *imapc.Client) (string, error) {
	var m model.IndexMailbox
	if err := u.idx.DB().Where("delimiter <> ''").First(&m).Error; err == nil {
		return m.Delimiter, nil
	}
	var delim string
	_, err := c.Execute(ctx, "LIST", func(r *imapc.Response) {
		if r.Type == "LIST" && len(r.Fields) > 1 {
			delim = r.Fields[1].String()
		}
	}, "", "")
	return delim, err
}

// RenameMailbox renames and/or moves a folder; children follow on the server and
// in the index, and every folder keeps its id.
func (u *Upstream) RenameMailbox(ctx context.Context, mailboxID, parentID, name string) (map[string]string, error) {
	m, err := u.mailbox(mailboxID)
	if err != nil {
		return nil, err
	}
	return u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		full, _, err := u.childName(ctx, c, parentID, name)
		if err != nil {
			return err
		}
		if full == m.Name {
			return nil
		}
		if err := c.Rename(ctx, m.Name, full); err != nil {
			return err
		}
		_ = c.Subscribe(ctx, full)
		old := m.Name
		return u.commit(states, func(tx *mailindex.Tx) error {
			all, err := tx.Mailboxes()
			if err != nil {
				return err
			}
			for i := range all {
				x := &all[i]
				switch {
				case x.ID == m.ID:
					x.Name, x.ParentID = full, parentID
				case m.Delimiter != "" && strings.HasPrefix(x.Name, old+m.Delimiter):
					x.Name = full + strings.TrimPrefix(x.Name, old)
				default:
					continue
				}
				if err := tx.UpdateMailbox(x, true); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// DeleteMailbox deletes a folder and the emails in it.
func (u *Upstream) DeleteMailbox(ctx context.Context, mailboxID string) (map[string]string, error) {
	m, err := u.mailbox(mailboxID)
	if err != nil {
		return nil, err
	}
	return u.writeOp(ctx, func(c *imapc.Client, states map[string]string) error {
		if sel := c.Selected(); sel != nil && sel.Name == m.Name {
			_ = c.Unselect(ctx)
		}
		if err := c.Delete(ctx, m.Name); err != nil {
			return err
		}
		u.forget(m.ID)
		return u.commit(states, func(tx *mailindex.Tx) error { return tx.DestroyMailbox(m.ID) })
	})
}

// FetchRaw streams a whole message.
func (u *Upstream) FetchRaw(ctx context.Context, emailID string, w io.Writer) error {
	return u.FetchSection(ctx, emailID, "", w)
}

// FetchSection streams BODY.PEEK[section] of an email; it does not take the op
// lock, so reading never waits for a sync round.
func (u *Upstream) FetchSection(ctx context.Context, emailID, section string, w io.Writer) error {
	var ms model.IndexMembership
	if err := u.idx.DB().First(&ms, "email_id = ?", emailID).Error; err != nil {
		return rejected("fetch", fmt.Errorf("%w: email %s", ErrNotFound, emailID))
	}
	m, err := u.mailbox(ms.MailboxID)
	if err != nil {
		return err
	}
	return u.withConn(ctx, func(c *imapc.Client) error {
		if sel := c.Selected(); sel == nil || sel.Name != m.Name {
			if _, err := c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
				return err
			}
		}
		found, err := c.FetchBody(ctx, ms.UID, section, w)
		if err != nil {
			return err
		}
		if !found {
			return rejected("fetch", fmt.Errorf("%w: message gone from the server", ErrNotFound))
		}
		return nil
	})
}
