package imap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

const (
	previewLen   = 256
	previewBytes = 4096 // fetched from the text part to build the preview
)

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("unknown charset %q", charset)
	}
	return enc.NewDecoder().Reader(input), nil
}

// decodeHeader decodes RFC 2047 encoded words; raw 8-bit bytes are kept when valid UTF-8.
func decodeHeader(s string) string {
	if s == "" {
		return ""
	}
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		s = d
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return s
}

var msgIDPattern = regexp.MustCompile(`<([^<>\s]+)>`)

// messageIDs parses a list of <id> tokens.
func messageIDs(s string) []string {
	var out []string
	for _, m := range msgIDPattern.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func firstID(s string) string {
	ids := messageIDs(s)
	if len(ids) == 0 {
		return strings.Trim(strings.TrimSpace(s), "<>")
	}
	return ids[0]
}

func addresses(list []imapc.Address) []mailindex.Address {
	out := make([]mailindex.Address, 0, len(list))
	for _, a := range list {
		out = append(out, mailindex.Address{Name: decodeHeader(a.Name), Email: a.Email()})
	}
	return out
}

// referencesHeader extracts the References field from a HEADER.FIELDS section.
func referencesHeader(sections map[string]string) []string {
	for k, v := range sections {
		if strings.HasPrefix(k, "HEADER.FIELDS") {
			msg, err := mail.ReadMessage(strings.NewReader(v + "\r\n"))
			if err != nil {
				return messageIDs(v)
			}
			return messageIDs(msg.Header.Get("References"))
		}
	}
	return nil
}

// toEmail converts fetched metadata to an index row. refs are the Message-IDs
// the message points at (In-Reply-To + References), for threading.
func toEmail(m *imapc.Message, key, threadKey string) (*model.IndexEmail, []string) {
	e := &model.IndexEmail{
		UpstreamID:       key,
		UpstreamThreadID: threadKey,
		Size:             m.Size,
		ReceivedAt:       m.InternalDate.UTC(),
		Keywords:         mailindex.FlagsToKeywords(m.Flags).JSON(),
		InReplyTo:        "[]",
		References:       "[]",
		From:             "[]", Sender: "[]", ReplyTo: "[]", To: "[]", Cc: "[]", Bcc: "[]",
	}
	kw := mailindex.ParseKeywords(e.Keywords)
	e.Seen, e.Flagged, e.Draft, e.Answered = kw["$seen"], kw["$flagged"], kw["$draft"], kw["$answered"]
	var refs []string
	if env := m.Envelope; env != nil {
		e.Subject = decodeHeader(env.Subject)
		e.MessageID = firstID(env.MessageID)
		e.From = mailindex.EncodeJSON(addresses(env.From))
		e.Sender = mailindex.EncodeJSON(addresses(env.Sender))
		e.ReplyTo = mailindex.EncodeJSON(addresses(env.ReplyTo))
		e.To = mailindex.EncodeJSON(addresses(env.To))
		e.Cc = mailindex.EncodeJSON(addresses(env.Cc))
		e.Bcc = mailindex.EncodeJSON(addresses(env.Bcc))
		irt := messageIDs(env.InReplyTo)
		e.InReplyTo = mailindex.EncodeJSON(irt)
		refs = append(refs, irt...)
		if t, err := mail.ParseDate(env.Date); err == nil {
			t = t.UTC()
			e.SentAt = &t
		}
	}
	if r := referencesHeader(m.Sections); len(r) > 0 {
		e.References = mailindex.EncodeJSON(r)
		refs = append(refs, r...)
	}
	if e.ReceivedAt.IsZero() {
		if e.SentAt != nil {
			e.ReceivedAt = *e.SentAt
		} else {
			e.ReceivedAt = time.Now().UTC()
		}
	}
	if m.Body != nil {
		b, _ := json.Marshal(m.Body)
		e.BodyStructure = string(b)
		e.HasAttachment = hasAttachment(m.Body, false)
	}
	return e, refs
}

// fallbackKey identifies a message without a stable server id, for matching an
// external move: Message-ID, size and INTERNALDATE.
func fallbackKey(msgID string, size int64, received time.Time) string {
	return msgID + "|" + strconv.FormatInt(size, 10) + "|" + strconv.FormatInt(received.UTC().Unix(), 10)
}

// hasAttachment: a part with disposition attachment, or a named non-text part
// that is not an inline image of an HTML body (multipart/related).
func hasAttachment(p *imapc.BodyPart, inRelated bool) bool {
	if p.Type == "multipart" {
		related := p.Subtype == "related"
		for _, c := range p.Parts {
			if hasAttachment(c, related) {
				return true
			}
		}
		return false
	}
	if p.Type == "message" && len(p.Parts) > 0 {
		return true // a forwarded message counts as an attachment
	}
	if p.Disposition == "attachment" {
		return true
	}
	if inRelated {
		return false // inline image of an HTML body
	}
	if p.Type == "text" {
		return p.Filename() != "" && p.Disposition != "inline"
	}
	return true // any other leaf outside multipart/related
}

// previewPart picks the part the preview is built from: the first text/plain
// that is not an attachment, else the first such text/html.
func previewPart(p *imapc.BodyPart) *imapc.BodyPart {
	var plain, htmlPart *imapc.BodyPart
	p.Walk(func(x *imapc.BodyPart) {
		if x.Type != "text" || x.Disposition == "attachment" || x.Filename() != "" {
			return
		}
		switch x.Subtype {
		case "plain":
			if plain == nil {
				plain = x
			}
		case "html":
			if htmlPart == nil {
				htmlPart = x
			}
		}
	})
	if plain != nil {
		return plain
	}
	return htmlPart
}

var (
	tagPattern   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]*>`)
	spacePattern = regexp.MustCompile(`\s+`)
)

// makePreview decodes a (possibly truncated) part and returns up to 256 characters.
func makePreview(p *imapc.BodyPart, raw string) string {
	data := []byte(raw)
	switch p.Encoding {
	case "base64":
		clean := bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, data)
		clean = clean[:len(clean)/4*4]
		dec := make([]byte, base64.StdEncoding.DecodedLen(len(clean)))
		n, _ := base64.StdEncoding.Decode(dec, clean)
		data = dec[:n]
	case "quoted-printable":
		dec, _ := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		data = dec
	}
	text := string(data)
	if cs := p.Params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") && !strings.EqualFold(cs, "us-ascii") {
		if r, err := charsetReader(cs, bytes.NewReader(data)); err == nil {
			if b, err := io.ReadAll(r); err == nil {
				text = string(b)
			}
		}
	}
	if p.Subtype == "html" {
		text = html.UnescapeString(tagPattern.ReplaceAllString(text, " "))
	}
	text = strings.ToValidUTF8(text, "")
	text = strings.TrimSpace(spacePattern.ReplaceAllString(text, " "))
	if utf8.RuneCountInString(text) > previewLen {
		r := []rune(text)
		text = string(r[:previewLen])
	}
	return text
}
