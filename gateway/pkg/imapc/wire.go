package imapc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind is the type of a parsed IMAP value.
type Kind uint8

// Value kinds. Numbers are atoms made of digits; their text is kept in Str too.
const (
	KindAtom Kind = iota
	KindNumber
	KindString // quoted string or literal
	KindList
	KindNil
)

// Value is one token of a server response. Responses are parsed into this
// generic tree first and interpreted afterwards, so extensions the client does
// not know never break the connection.
type Value struct {
	Kind Kind
	Str  string
	Num  uint64
	List []Value
	// Streamed is true for a literal that went to a LiteralSink instead of Str.
	Streamed bool
}

// IsAtom reports whether v is the atom s (case-insensitive).
func (v Value) IsAtom(s string) bool { return v.Kind == KindAtom && strings.EqualFold(v.Str, s) }

// String returns the text of an atom, number or string; "" for NIL and lists.
func (v Value) String() string {
	if v.Kind == KindList || v.Kind == KindNil {
		return ""
	}
	return v.Str
}

// Response is one server response line, with literals inlined.
type Response struct {
	// Tag is "*" for untagged data, "+" for a continuation request, else the command tag.
	Tag string
	// Num is the number of "* 12 EXISTS" style responses; HasNum tells it is set.
	Num    uint32
	HasNum bool
	// Type is upper-case: OK NO BAD BYE PREAUTH CAPABILITY LIST STATUS FETCH EXISTS ...
	Type string
	// Code and CodeArgs are the response code of a status response: "[UIDNEXT 5]".
	Code     string
	CodeArgs []Value
	// Text is the human text of a status response.
	Text string
	// Fields are the parsed values after Type, for data responses.
	Fields []Value
}

func (r *Response) isStatus() bool {
	switch r.Type {
	case "OK", "NO", "BAD", "BYE", "PREAUTH":
		return true
	}
	return false
}

// LiteralSink lets a command stream a large literal (a message body) instead of
// holding it in memory. It returns nil to keep the literal in Value.Str.
type LiteralSink func(size int64) io.Writer

// maxInlineLiteral bounds literals held in memory; bigger ones need a sink.
const maxInlineLiteral = 64 << 20

type reader struct {
	br   *bufio.Reader
	sink LiteralSink
}

var errProtocol = errors.New("imapc: malformed server response")

func protoErr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errProtocol}, args...)...)
}

// readResponse reads one complete response.
func (r *reader) readResponse() (*Response, error) {
	tag, err := r.readUntilSP()
	if err != nil {
		return nil, err
	}
	resp := &Response{Tag: tag}
	if tag == "+" {
		resp.Text, err = r.readLine()
		return resp, err
	}
	word, err := r.readUntilSP()
	if err != nil {
		return nil, err
	}
	if tag == "*" {
		if n, err := strconv.ParseUint(word, 10, 32); err == nil {
			resp.Num, resp.HasNum = uint32(n), true
			if word, err = r.readUntilSP(); err != nil {
				return nil, err
			}
		}
	}
	resp.Type = strings.ToUpper(word)
	if resp.isStatus() {
		return resp, r.readStatusRest(resp)
	}
	resp.Fields, err = r.readValues()
	return resp, err
}

// readUntilSP reads a word ending at a space or the end of the line. At the end
// of the line the CRLF is left in place for the caller.
func (r *reader) readUntilSP() (string, error) {
	var sb strings.Builder
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case ' ':
			return sb.String(), nil
		case '\r', '\n':
			_ = r.br.UnreadByte()
			return sb.String(), nil
		}
		sb.WriteByte(b)
	}
}

func (r *reader) readLine() (string, error) {
	line, err := r.br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readStatusRest parses "[CODE args] text" up to the end of the line.
func (r *reader) readStatusRest(resp *Response) error {
	b, err := r.br.Peek(1)
	if err != nil {
		return err
	}
	if b[0] == '[' {
		r.br.ReadByte()
		code, err := r.readCodeAtom()
		if err != nil {
			return err
		}
		resp.Code = strings.ToUpper(code)
		for {
			r.skipSpaces()
			b, err := r.br.Peek(1)
			if err != nil {
				return err
			}
			if b[0] == ']' {
				r.br.ReadByte()
				break
			}
			if b[0] == '\r' || b[0] == '\n' {
				break // unterminated code: tolerate
			}
			v, err := r.readValue(true)
			if err != nil {
				return err
			}
			resp.CodeArgs = append(resp.CodeArgs, v)
		}
		r.skipSpaces()
	}
	resp.Text, err = r.readLine()
	return err
}

func (r *reader) readCodeAtom() (string, error) {
	var sb strings.Builder
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return "", err
		}
		if b == ' ' || b == ']' || b == '\r' || b == '\n' {
			_ = r.br.UnreadByte()
			return sb.String(), nil
		}
		sb.WriteByte(b)
	}
}

func (r *reader) skipSpaces() {
	for {
		b, err := r.br.Peek(1)
		if err != nil || b[0] != ' ' {
			return
		}
		r.br.ReadByte()
	}
}

// readValues reads values to the end of the line and consumes the CRLF.
func (r *reader) readValues() ([]Value, error) {
	var out []Value
	for {
		r.skipSpaces()
		b, err := r.br.Peek(1)
		if err != nil {
			return nil, err
		}
		if b[0] == '\r' || b[0] == '\n' {
			_, err := r.readLine()
			return out, err
		}
		v, err := r.readValue(false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// readValue reads one value. inCode stops atoms at ']' (response codes).
func (r *reader) readValue(inCode bool) (Value, error) {
	b, err := r.br.Peek(1)
	if err != nil {
		return Value{}, err
	}
	switch b[0] {
	case '(':
		r.br.ReadByte()
		var list []Value
		for {
			r.skipSpaces()
			b, err := r.br.Peek(1)
			if err != nil {
				return Value{}, err
			}
			if b[0] == ')' {
				r.br.ReadByte()
				return Value{Kind: KindList, List: list}, nil
			}
			if b[0] == '\r' || b[0] == '\n' {
				return Value{}, protoErr("unterminated list")
			}
			v, err := r.readValue(inCode)
			if err != nil {
				return Value{}, err
			}
			list = append(list, v)
		}
	case '"':
		r.br.ReadByte()
		s, err := r.readQuoted()
		return Value{Kind: KindString, Str: s}, err
	case '{':
		return r.readLiteral()
	}
	atom, err := r.readAtom(inCode)
	if err != nil {
		return Value{}, err
	}
	if atom == "" {
		c, _ := r.br.ReadByte()
		return Value{}, protoErr("unexpected %q", c)
	}
	if strings.EqualFold(atom, "NIL") {
		return Value{Kind: KindNil}, nil
	}
	if n, err := strconv.ParseUint(atom, 10, 64); err == nil {
		return Value{Kind: KindNumber, Num: n, Str: atom}, nil
	}
	return Value{Kind: KindAtom, Str: atom}, nil
}

func (r *reader) readQuoted() (string, error) {
	var sb strings.Builder
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '"':
			return sb.String(), nil
		case '\\':
			b, err = r.br.ReadByte()
			if err != nil {
				return "", err
			}
		case '\r', '\n':
			return "", protoErr("newline in quoted string")
		}
		sb.WriteByte(b)
	}
}

func (r *reader) readLiteral() (Value, error) {
	r.br.ReadByte() // {
	var digits strings.Builder
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return Value{}, err
		}
		if b == '}' {
			break
		}
		if b == '+' || b == '~' {
			continue
		}
		digits.WriteByte(b)
	}
	if _, err := r.readLine(); err != nil {
		return Value{}, err
	}
	n, err := strconv.ParseInt(digits.String(), 10, 64)
	if err != nil || n < 0 {
		return Value{}, protoErr("bad literal size %q", digits.String())
	}
	if r.sink != nil {
		if w := r.sink(n); w != nil {
			if _, err := io.CopyN(w, r.br, n); err != nil {
				return Value{}, err
			}
			return Value{Kind: KindString, Streamed: true}, nil
		}
	}
	if n > maxInlineLiteral {
		return Value{}, protoErr("literal of %d bytes too large", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r.br, buf); err != nil {
		return Value{}, err
	}
	return Value{Kind: KindString, Str: string(buf)}, nil
}

// readAtom reads an atom. A "[...]" segment is taken whole, spaces included, so
// BODY[HEADER.FIELDS (REFERENCES)]<0> is one atom; so is a "<origin>" suffix.
func (r *reader) readAtom(inCode bool) (string, error) {
	var sb strings.Builder
	for {
		b, err := r.br.Peek(1)
		if err != nil {
			return sb.String(), err
		}
		c := b[0]
		switch {
		case c == '[' && !inCode:
			r.br.ReadByte()
			sb.WriteByte(c)
			depth := 1
			for depth > 0 {
				c, err := r.br.ReadByte()
				if err != nil {
					return "", err
				}
				if c == '\r' || c == '\n' {
					return "", protoErr("unterminated section")
				}
				switch c {
				case '[':
					depth++
				case ']':
					depth--
				}
				sb.WriteByte(c)
			}
			continue
		case c == ' ' || c == '(' || c == ')' || c == '{' || c == '"' || c == '\r' || c == '\n':
			return sb.String(), nil
		case c == ']' && inCode:
			return sb.String(), nil
		}
		r.br.ReadByte()
		sb.WriteByte(c)
	}
}
