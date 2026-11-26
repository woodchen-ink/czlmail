package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// message is one email as the methods see it: its index row and, loaded on
// demand, its body structure and sections.
type message struct {
	row       model.IndexEmail
	mailboxes []string

	body *bodyLists
	raw  *rawMessage // parsed locally when the index has no BODYSTRUCTURE
}

// loadMessages reads the index rows of ids (orphans count as missing).
func loadMessages(idx *mailindex.Store, ids []string) (map[string]*message, error) {
	out := make(map[string]*message, len(ids))
	for part := range chunk(ids, 500) {
		var rows []model.IndexEmail
		if err := idx.DB().Where("id IN ? AND orphaned_from = ''", part).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = &message{row: r}
		}
		var ms []model.IndexMembership
		// Hidden mailboxes (Gmail's All Mail) are not in mailboxIds.
		if err := idx.DB().Table("memberships m").Select("m.*").Joins("JOIN mailboxes b ON b.id = m.mailbox_id").
			Where("m.email_id IN ? AND b.hidden = ?", part, false).Scan(&ms).Error; err != nil {
			return nil, err
		}
		for _, m := range ms {
			if e := out[m.EmailID]; e != nil {
				e.mailboxes = append(e.mailboxes, m.MailboxID)
			}
		}
	}
	return out, nil
}

func chunk[T any](s []T, n int) func(func([]T) bool) {
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

// maxSectionInMemory bounds a header or text part read for Email/get.
const maxSectionInMemory = 32 << 20

// maxRawParse is the largest message parsed locally (servers without BODYSTRUCTURE).
const maxRawParse = 64 << 20

// structure returns the body lists, parsing the whole message when the server
// gave no BODYSTRUCTURE.
func (s *Service) structure(ctx context.Context, a *account, m *message) (*bodyLists, error) {
	if m.body != nil {
		return m.body, nil
	}
	var root *imapc.BodyPart
	if m.row.BodyStructure != "" {
		root = &imapc.BodyPart{}
		if err := json.Unmarshal([]byte(m.row.BodyStructure), root); err != nil {
			root = nil
		}
	}
	if root == nil {
		// No BODYSTRUCTURE: parse the whole message. One beyond the largest
		// message the gateway accepts tells a truncated read apart.
		raw, err := s.readSection(ctx, a, m, "", maxRawParse+1)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxRawParse {
			return nil, errorf("message %s is too large to parse without BODYSTRUCTURE", m.row.ID)
		}
		m.raw = parseRawMessage(raw)
		root = m.raw.root
	}
	m.body = buildBody(root)
	return m.body, nil
}

// readSection reads a whole section into memory; limit 0 means maxSectionInMemory.
func (s *Service) readSection(ctx context.Context, a *account, m *message, section string, limit int64) ([]byte, error) {
	if m.raw != nil && section != "" {
		return m.raw.sections[section], nil
	}
	if limit <= 0 {
		limit = maxSectionInMemory
	}
	f, err := s.openSection(ctx, a, m.row.ID, section)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// openSection opens BODY[section] of an email through the cache.
func (s *Service) openSection(ctx context.Context, a *account, emailID, section string) (*File, error) {
	return s.cache.Open(ctx, cacheKey(a.id, emailID, section), func(w io.Writer) error {
		if section == "" {
			return a.up.FetchRaw(ctx, emailID, w)
		}
		return a.up.FetchSection(ctx, emailID, section, w)
	})
}

// decodeTransfer removes a Content-Transfer-Encoding. problem is set when the
// data was not valid in that encoding (what decoded is still returned).
func decodeTransfer(data []byte, encoding string) (out []byte, problem bool) {
	switch strings.ToLower(encoding) {
	case "base64":
		clean, _ := io.ReadAll(&base64Only{r: bytes.NewReader(data)})
		dec := make([]byte, base64.RawStdEncoding.DecodedLen(len(clean)))
		n, err := base64.RawStdEncoding.Decode(dec, clean)
		// Anything besides the alphabet, blanks and padding is a problem worth reporting.
		junk := len(bytes.TrimRight(bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, data), "=")) != len(clean)
		return dec[:n], err != nil || junk
	case "quoted-printable":
		dec, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		return dec, err != nil
	}
	return data, false
}

// transferReader is the streaming form of decodeTransfer, for blob downloads.
func transferReader(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(encoding) {
	case "base64":
		return base64.NewDecoder(base64.RawStdEncoding, &base64Only{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// base64Only keeps the base64 alphabet and drops everything else (line
// breaks, padding, stray bytes), as decodeTransfer does: real mail often has
// junk the strict decoder would stop at in the middle of a download.
type base64Only struct{ r io.Reader }

func (s *base64Only) Read(p []byte) (int, error) {
	for {
		n, err := s.r.Read(p)
		j := 0
		for _, b := range p[:n] {
			if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/' {
				p[j] = b
				j++
			}
		}
		if j > 0 || err != nil {
			return j, err
		}
	}
}

// decodeCharset converts text to UTF-8. problem is set for an unknown charset
// or bytes invalid in it.
func decodeCharset(data []byte, charset string) (string, bool) {
	cs := strings.ToLower(strings.TrimSpace(charset))
	problem := false
	if cs != "" && cs != "utf-8" && cs != "us-ascii" && cs != "utf8" {
		if enc, err := htmlindex.Get(cs); err == nil {
			if out, err := enc.NewDecoder().Bytes(data); err == nil {
				data = out
			} else {
				problem = true
			}
		} else {
			problem = true
		}
	}
	if !utf8.Valid(data) {
		return strings.ToValidUTF8(string(data), "�"), true
	}
	return string(data), problem
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// messageGone reports an upstream answer meaning the message is no longer on
// the server (the next sync removes it from the index).
func messageGone(err error) bool {
	return errors.Is(err, upstream.ErrNotFound)
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }
