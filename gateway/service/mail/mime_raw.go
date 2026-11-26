package mail

import (
	"bytes"
	"mime"
	"net/textproto"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
)

// rawMessage is a message parsed locally, for servers that do not send
// BODYSTRUCTURE: the same tree imapc builds, and every IMAP section it can name.
type rawMessage struct {
	root     *imapc.BodyPart
	sections map[string][]byte // "HEADER", "<path>" (body, still encoded), "<path>.MIME"
}

func parseRawMessage(raw []byte) *rawMessage {
	rm := &rawMessage{sections: map[string][]byte{}}
	head, body := splitHeader(raw)
	rm.sections["HEADER"] = head
	rm.root = rm.parsePart(head, body, "", true)
	return rm
}

// splitHeader cuts a part into its header (with the blank line) and body.
func splitHeader(b []byte) (head, body []byte) {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return b[:i+4], b[i+4:]
	}
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return b[:i+2], b[i+2:]
	}
	return b, nil
}

func parseHeaderFields(head []byte) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	for _, f := range headerFields(head) {
		k := textproto.CanonicalMIMEHeaderKey(f.name)
		h[k] = append(h[k], strings.TrimSpace(unfold(f.value)))
	}
	return h
}

// parsePart builds the node of one part. path is its section number; the root
// of a single-part message is "1", of a multipart message "".
func (rm *rawMessage) parsePart(head, body []byte, path string, root bool) *imapc.BodyPart {
	h := parseHeaderFields(head)
	ctype := h.Get("Content-Type")
	if ctype == "" {
		ctype = "text/plain"
	}
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mt, params = "text/plain", map[string]string{}
	}
	typ, sub, _ := strings.Cut(strings.ToLower(mt), "/")
	p := &imapc.BodyPart{Type: typ, Subtype: sub, Params: params}
	if disp := h.Get("Content-Disposition"); disp != "" {
		if d, dp, err := mime.ParseMediaType(disp); err == nil {
			p.Disposition, p.DispParams = strings.ToLower(d), dp
		}
	}
	p.ID = h.Get("Content-Id")
	p.Description = h.Get("Content-Description")
	p.Location = h.Get("Content-Location")
	if l := h.Get("Content-Language"); l != "" {
		for _, x := range strings.Split(l, ",") {
			p.Language = append(p.Language, strings.TrimSpace(x))
		}
	}
	p.Encoding = strings.ToLower(strings.TrimSpace(h.Get("Content-Transfer-Encoding")))
	if typ == "multipart" && params["boundary"] != "" {
		p.Path = path
		if !root {
			rm.sections[path+".MIME"] = head
		}
		for i, part := range splitMultipart(body, params["boundary"]) {
			ph, pb := splitHeader(part)
			cp := childSection(path, i+1)
			p.Parts = append(p.Parts, rm.parsePart(ph, pb, cp, false))
		}
		return p
	}
	if root {
		path = "1"
	} else {
		rm.sections[path+".MIME"] = head
	}
	p.Path = path
	p.Size = int64(len(body))
	rm.sections[path] = body
	return p
}

func childSection(parent string, n int) string {
	if parent == "" {
		return itoa(n)
	}
	return parent + "." + itoa(n)
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// splitMultipart returns the raw parts between the boundary lines.
func splitMultipart(body []byte, boundary string) [][]byte {
	delim := []byte("--" + boundary)
	var parts [][]byte
	var cur []byte
	in := false
	for len(body) > 0 {
		line := body
		next := []byte(nil)
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			line, next = body[:i+1], body[i+1:]
		}
		trimmed := bytes.TrimRight(line, " \t\r\n")
		switch {
		case bytes.Equal(trimmed, delim):
			if in {
				parts = append(parts, trimEOL(cur))
			}
			in, cur = true, nil
		case bytes.Equal(trimmed, append(delim, '-', '-')):
			if in {
				parts = append(parts, trimEOL(cur))
			}
			return parts
		case in:
			cur = append(cur, line...)
		}
		body = next
	}
	if in {
		parts = append(parts, trimEOL(cur))
	}
	return parts
}

// trimEOL drops the line break that belongs to the next boundary line.
func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}
