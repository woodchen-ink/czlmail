package imap

import (
	"context"
	"strconv"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// GmailRawQuery translates a search filter to Gmail search syntax for X-GM-RAW.
// Every condition is ANDed, as in a JMAP FilterCondition. It returns "" when
// the filter has nothing for the server.
func GmailRawQuery(f upstream.SearchFilter) string {
	var parts []string
	field := func(op, v string) {
		if v = gmSanitize(v); v != "" {
			parts = append(parts, op+":"+gmPhrase(v))
		}
	}
	words := func(v string) {
		if v = gmSanitize(v); v != "" {
			parts = append(parts, strings.Fields(v)...)
		}
	}
	words(f.Text)
	words(f.Body) // Gmail has no body: operator; plain words search the text
	field("from", f.From)
	field("to", f.To)
	field("cc", f.Cc)
	field("bcc", f.Bcc)
	field("subject", f.Subject)
	if f.After != nil {
		parts = append(parts, "after:"+strconv.FormatInt(f.After.Unix()-1, 10)) // after: is exclusive
	}
	if f.Before != nil {
		parts = append(parts, "before:"+strconv.FormatInt(f.Before.Unix(), 10))
	}
	if f.MinSize > 0 {
		parts = append(parts, "larger:"+strconv.FormatInt(f.MinSize-1, 10))
	}
	if f.MaxSize > 0 {
		parts = append(parts, "smaller:"+strconv.FormatInt(f.MaxSize, 10))
	}
	if f.HasAttachment != nil {
		if *f.HasAttachment {
			parts = append(parts, "has:attachment")
		} else {
			parts = append(parts, "-has:attachment")
		}
	}
	return strings.Join(parts, " ")
}

// gmSanitize drops characters that are Gmail search syntax, so a value cannot
// change the query's structure.
func gmSanitize(v string) string {
	v = strings.Map(func(r rune) rune {
		switch r {
		case '"', '(', ')', '{', '}', '\r', '\n', '\t':
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// gmPhrase quotes a value with spaces: from:"Ann Lee".
func gmPhrase(v string) string {
	if strings.ContainsAny(v, " :") || strings.HasPrefix(v, "-") {
		return `"` + v + `"`
	}
	return v
}

// SearchText runs X-GM-RAW in All Mail, Trash and Spam and returns the indexed
// emails that match; see upstream.TextSearcher.
func (g *Gmail) SearchText(ctx context.Context, f upstream.SearchFilter) ([]string, error) {
	q := GmailRawQuery(f)
	if q == "" {
		return nil, nil
	}
	var folders []model.IndexMailbox
	if err := g.idx.DB().Where("virtual = ? AND no_select = ?", false, false).Order("sort_order").Find(&folders).Error; err != nil {
		return nil, err
	}
	var out []string
	err := g.withConn(ctx, func(c *imapc.Client) error {
		for _, m := range folders {
			if _, err := c.Select(ctx, m.Name, imapc.SelectOptions{ReadOnly: true}); err != nil {
				if isOperational(err) {
					continue
				}
				return err
			}
			uids, err := c.SearchUIDs(ctx, imapc.Atom("X-GM-RAW"), q)
			if err != nil {
				return err
			}
			for chunk := range chunkUIDs(uids, 500) {
				var ids []string
				if err := g.idx.DB().Model(&model.IndexMembership{}).Where("mailbox_id = ? AND uid IN ?", m.ID, chunk).Pluck("email_id", &ids).Error; err != nil {
					return err
				}
				out = append(out, ids...)
			}
		}
		return nil
	})
	return out, err
}

func chunkUIDs(s imapc.UIDSet, n int) func(func([]uint32) bool) {
	return func(yield func([]uint32) bool) {
		for i := 0; i < len(s); i += n {
			if !yield(s[i:min(i+n, len(s))]) {
				return
			}
		}
	}
}
