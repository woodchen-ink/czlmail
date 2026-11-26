package imap

import (
	"context"
	"fmt"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Search runs UID SEARCH in one folder and maps the UIDs to email ids. It does
// not take the op lock: a search never waits for a sync round.
func (u *Upstream) Search(ctx context.Context, mailboxID string, q upstream.SearchQuery) ([]string, error) {
	m, err := u.mailbox(mailboxID)
	if err != nil {
		return nil, err
	}
	if m.NoSelect {
		return nil, nil
	}
	build := u.opts.Dialect.SearchCriteria
	if build == nil {
		build = searchCriteria
	}
	criteria, utf8 := build(q)
	if len(criteria) == 0 {
		criteria = []any{imapc.Atom("ALL")}
	}
	if utf8 {
		criteria = append([]any{imapc.Atom("CHARSET"), imapc.Atom("UTF-8")}, criteria...)
	}
	var uids imapc.UIDSet
	err = u.withConn(ctx, func(c *imapc.Client) error {
		if sel := c.Selected(); sel == nil || sel.Name != m.Name {
			if _, err := c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
				return err
			}
		}
		var err error
		uids, err = c.SearchUIDs(ctx, criteria...)
		return err
	})
	if err != nil || len(uids) == 0 {
		return nil, err
	}
	var ids []string
	for part := range chunks(uids, 900) {
		var got []string
		err := u.idx.DB().Model(&model.IndexMembership{}).Where("mailbox_id = ? AND uid IN ?", m.ID, []uint32(part)).
			Pluck("email_id", &got).Error
		if err != nil {
			return nil, fmt.Errorf("map search uids: %w", err)
		}
		ids = append(ids, got...)
	}
	return ids, nil
}

// searchCriteria is the standard IMAP form of q: every key ANDed.
func searchCriteria(q upstream.SearchQuery) ([]any, bool) {
	var out []any
	utf8 := false
	add := func(key, v string) {
		out = append(out, imapc.Atom(key), v)
		utf8 = utf8 || !isASCII(v)
	}
	for _, v := range q.Text {
		add("TEXT", v)
	}
	for _, v := range q.Body {
		add("BODY", v)
	}
	for _, v := range q.Bcc {
		add("BCC", v)
	}
	for _, h := range q.Header {
		if len(h) == 0 {
			continue
		}
		value := ""
		if len(h) > 1 {
			value = h[1]
		}
		out = append(out, imapc.Atom("HEADER"), h[0], value)
		utf8 = utf8 || !isASCII(h[0]) || !isASCII(value)
	}
	return out, utf8
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func chunks[T any](s []T, n int) func(func([]T) bool) {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			k := min(n, len(s))
			if !yield(s[:k]) {
				return
			}
			s = s[k:]
		}
	}
}
