package mail

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
)

// headerField is one header line as sent: value is everything after the colon,
// folding kept (RFC 8621 Raw form).
type headerField struct {
	name  string
	value string
}

// headerFields splits a header block into its fields, in order.
func headerFields(head []byte) []headerField {
	var out []headerField
	lines := bytes.Split(head, []byte("\n"))
	for _, line := range lines {
		l := string(line)
		if strings.TrimRight(l, "\r") == "" {
			continue
		}
		if (l[0] == ' ' || l[0] == '\t') && len(out) > 0 {
			out[len(out)-1].value += "\n" + l
			continue
		}
		name, value, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		out = append(out, headerField{name: strings.TrimSpace(name), value: value})
	}
	for i := range out {
		v := strings.ReplaceAll(out[i].value, "\r", "")
		v = strings.ReplaceAll(v, "\n", "\r\n")
		out[i].value = strings.TrimRight(validUTF8(v), "\r\n")
	}
	return out
}

func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// unfold removes folding line breaks.
func unfold(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "")
	return strings.ReplaceAll(s, "\n", "")
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("unknown charset %q", charset)
	}
	return enc.NewDecoder().Reader(input), nil
}

// decodeWords decodes RFC 2047 encoded words.
func decodeWords(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return validUTF8(d)
	}
	return validUTF8(s)
}

// headerProp is a parsed "header:{name}[:as{Form}][:all]" property.
type headerProp struct {
	name string // lower-case
	form string // Raw, Text, Addresses, GroupedAddresses, MessageIds, Date, URLs
	all  bool
}

var headerForms = map[string]bool{"Raw": true, "Text": true, "Addresses": true, "GroupedAddresses": true,
	"MessageIds": true, "Date": true, "URLs": true}

func parseHeaderProp(p string) (headerProp, bool) {
	rest, ok := strings.CutPrefix(p, "header:")
	if !ok || rest == "" {
		return headerProp{}, false
	}
	parts := strings.Split(rest, ":")
	hp := headerProp{name: strings.ToLower(parts[0]), form: "Raw"}
	for _, x := range parts[1:] {
		switch {
		case x == "all" && !hp.all:
			hp.all = true
		case strings.HasPrefix(x, "as") && headerForms[x[2:]] && hp.form == "Raw" && !hp.all:
			hp.form = x[2:]
		default:
			return headerProp{}, false
		}
	}
	if hp.name == "" || strings.ContainsAny(hp.name, " \t") {
		return headerProp{}, false
	}
	return hp, true
}

// value computes the property from a header block.
func (hp headerProp) value(fields []headerField) any {
	var vals []any
	for _, f := range fields {
		if strings.EqualFold(f.name, hp.name) {
			vals = append(vals, convertHeader(f.value, hp.form))
		}
	}
	if hp.all {
		if vals == nil {
			return []any{}
		}
		return vals
	}
	if len(vals) == 0 {
		return nil
	}
	return vals[len(vals)-1] // the last instance (RFC 8621 §4.1.3)
}

type emailAddress struct {
	Name  *string `json:"name"`
	Email string  `json:"email"`
}

type addressGroup struct {
	Name      *string        `json:"name"`
	Addresses []emailAddress `json:"addresses"`
}

var addrParser = &mail.AddressParser{WordDecoder: wordDecoder}

func convertHeader(raw, form string) any {
	switch form {
	case "Raw":
		return raw
	case "Text":
		return strings.TrimSpace(decodeWords(unfold(raw)))
	case "Addresses":
		return parseAddresses(raw)
	case "GroupedAddresses":
		return []addressGroup{{Name: nil, Addresses: parseAddresses(raw)}}
	case "MessageIds":
		ids := msgIDs(unfold(raw))
		if ids == nil {
			return nil
		}
		return ids
	case "Date":
		t, err := mail.ParseDate(strings.TrimSpace(unfold(raw)))
		if err != nil {
			return nil
		}
		return t.Format(time.RFC3339)
	case "URLs":
		var out []string
		for _, m := range urlPattern.FindAllStringSubmatch(unfold(raw), -1) {
			out = append(out, m[1])
		}
		if out == nil {
			return nil
		}
		return out
	}
	return raw
}

var urlPattern = regexp.MustCompile(`<([^<>\s]+)>`)

func parseAddresses(raw string) []emailAddress {
	v := strings.TrimSpace(unfold(raw))
	out := []emailAddress{}
	if v == "" {
		return out
	}
	list, err := addrParser.ParseList(v)
	if err != nil {
		// Lenient fallback: one address per comma-separated item.
		for _, item := range strings.Split(v, ",") {
			if a, err := addrParser.Parse(strings.TrimSpace(item)); err == nil {
				list = append(list, a)
			}
		}
	}
	for _, a := range list {
		var name *string
		if a.Name != "" {
			n := validUTF8(a.Name)
			name = &n
		}
		out = append(out, emailAddress{Name: name, Email: a.Address})
	}
	return out
}

var msgIDPattern = regexp.MustCompile(`<([^<>\s]+)>`)

func msgIDs(s string) []string {
	var out []string
	for _, m := range msgIDPattern.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

type headerJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func headersJSON(fields []headerField) []headerJSON {
	out := make([]headerJSON, 0, len(fields))
	for _, f := range fields {
		out = append(out, headerJSON{Name: f.name, Value: f.value})
	}
	return out
}
