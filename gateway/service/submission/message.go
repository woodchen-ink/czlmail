package submission

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

var errInvalidMessage = errors.New("message headers cannot be parsed")

// header is the part of a draft's header the submission needs.
type header struct {
	From, Sender, To, Cc, Bcc []*mail.Address
	MessageID                 string // without angle brackets
	HasDate                   bool
}

var addressParser = &mail.AddressParser{WordDecoder: &mime.WordDecoder{
	// Only the address matters here; names in unknown charsets stay encoded.
	CharsetReader: func(_ string, r io.Reader) (io.Reader, error) { return r, nil },
}}

func parseHeader(raw []byte) (*header, error) {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw)))
	mh, err := tp.ReadMIMEHeader()
	if err != nil && !(errors.Is(err, io.EOF) && len(mh) > 0) {
		return nil, errInvalidMessage
	}
	h := &header{HasDate: mh.Get("Date") != ""}
	list := func(name string) ([]*mail.Address, error) {
		var out []*mail.Address
		for _, v := range mh.Values(name) {
			if strings.TrimSpace(v) == "" {
				continue
			}
			a, err := addressParser.ParseList(v)
			if err != nil {
				return nil, errInvalidMessage
			}
			out = append(out, a...)
		}
		return out, nil
	}
	for _, f := range []struct {
		name string
		dst  *[]*mail.Address
	}{{"From", &h.From}, {"Sender", &h.Sender}, {"To", &h.To}, {"Cc", &h.Cc}, {"Bcc", &h.Bcc}} {
		if *f.dst, err = list(f.name); err != nil {
			return nil, err
		}
	}
	h.MessageID = strings.Trim(strings.TrimSpace(mh.Get("Message-Id")), "<>")
	return h, nil
}

// headerEnd returns the offset of the blank line ending the header, and its length.
func headerEnd(raw []byte) (int, int) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return i + 2, 2
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return i + 1, 1
	}
	return len(raw), 0
}

// messageID is the draft's Message-ID, or a new one in the sender's domain.
func messageID(h *header, fromDomain string) string {
	if h.MessageID != "" {
		return h.MessageID
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	if fromDomain == "" {
		fromDomain = "gateway.invalid"
	}
	return hex.EncodeToString(b[:]) + "@" + fromDomain
}

// outgoing returns the message with msgID and a Date when the draft has none,
// and without Bcc (with its folded lines) when stripBcc is set.
func outgoing(raw []byte, h *header, msgID string, now time.Time, stripBcc bool) []byte {
	end, _ := headerEnd(raw)
	head, body := raw[:end], raw[end:]
	var out bytes.Buffer
	out.Grow(len(raw) + 128)
	if h.MessageID == "" {
		out.WriteString("Message-ID: <" + msgID + ">\r\n")
	}
	if !h.HasDate {
		out.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	}
	skipping := false
	for len(head) > 0 {
		line := head
		if i := bytes.IndexByte(head, '\n'); i >= 0 {
			line = head[:i+1]
		}
		head = head[len(line):]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if !skipping {
				out.Write(line)
			}
			continue
		}
		name, _, _ := bytes.Cut(line, []byte(":"))
		skipping = stripBcc && strings.EqualFold(strings.TrimSpace(string(name)), "Bcc")
		if !skipping {
			out.Write(line)
		}
	}
	out.Write(body)
	return out.Bytes()
}

func domainOf(addr string) string {
	_, d, _ := strings.Cut(addr, "@")
	return strings.ToLower(d)
}
