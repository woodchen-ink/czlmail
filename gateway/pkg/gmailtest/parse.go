package gmailtest

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// token is one parsed IMAP argument.
type token struct {
	kind tokKind
	s    string  // atom text or string value
	list []token // for tokList
}

type tokKind int

const (
	tokAtom   tokKind = iota
	tokString         // quoted or literal
	tokList
)

func (t token) isAtom(s string) bool { return t.kind == tokAtom && strings.EqualFold(t.s, s) }

var lineLiteral = regexp.MustCompile(`\{(\d+)(\+?)\}$`)

const maxLiteral = 50 << 20

// readCommand reads one command line including its literals. cont is called before
// each synchronising literal so the session can send "+".
func readCommand(br *bufio.Reader, cont func()) (string, error) {
	var buf strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		buf.WriteString(line)
		m := lineLiteral.FindStringSubmatch(line)
		if m == nil {
			return buf.String(), nil
		}
		n, _ := strconv.Atoi(m[1])
		if n > maxLiteral {
			return "", errors.New("literal too large")
		}
		if m[2] == "" {
			cont()
		}
		buf.WriteString("\r\n")
		lit := make([]byte, n)
		if _, err := io.ReadFull(br, lit); err != nil {
			return "", err
		}
		buf.Write(lit)
	}
}

// parser tokenises a command line produced by readCommand.
type parser struct {
	s   string
	pos int
}

func (p *parser) done() bool {
	p.skipSpace()
	return p.pos >= len(p.s)
}

func (p *parser) skipSpace() {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
}

// rest returns the unparsed remainder.
func (p *parser) rest() string {
	p.skipSpace()
	return p.s[p.pos:]
}

func (p *parser) next() (token, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return token{}, errors.New("missing argument")
	}
	switch c := p.s[p.pos]; c {
	case '(':
		p.pos++
		var list []token
		for {
			p.skipSpace()
			if p.pos >= len(p.s) {
				return token{}, errors.New("unclosed list")
			}
			if p.s[p.pos] == ')' {
				p.pos++
				return token{kind: tokList, list: list}, nil
			}
			t, err := p.next()
			if err != nil {
				return token{}, err
			}
			list = append(list, t)
		}
	case ')':
		return token{}, errors.New("unexpected )")
	case '"':
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) {
			c := p.s[p.pos]
			p.pos++
			switch c {
			case '\\':
				if p.pos < len(p.s) {
					b.WriteByte(p.s[p.pos])
					p.pos++
				}
			case '"':
				return token{kind: tokString, s: b.String()}, nil
			default:
				b.WriteByte(c)
			}
		}
		return token{}, errors.New("unclosed string")
	case '{':
		end := strings.Index(p.s[p.pos:], "}\r\n")
		if end < 0 {
			return token{}, errors.New("bad literal")
		}
		n, err := strconv.Atoi(strings.TrimSuffix(p.s[p.pos+1:p.pos+end], "+"))
		if err != nil {
			return token{}, errors.New("bad literal")
		}
		start := p.pos + end + 3
		if start+n > len(p.s) {
			return token{}, errors.New("short literal")
		}
		p.pos = start + n
		return token{kind: tokString, s: p.s[start : start+n]}, nil
	default:
		start := p.pos
		depth := 0
		for p.pos < len(p.s) {
			c := p.s[p.pos]
			if depth == 0 && (c == ' ' || c == '(' || c == ')') {
				break
			}
			switch c {
			case '[':
				depth++
			case ']':
				depth--
			}
			p.pos++
		}
		return token{kind: tokAtom, s: p.s[start:p.pos]}, nil
	}
}

// str reads an astring.
func (p *parser) str() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	if t.kind == tokList {
		return "", errors.New("expected string")
	}
	return t.s, nil
}

// all reads every remaining token.
func (p *parser) all() ([]token, error) {
	var out []token
	for !p.done() {
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// strings flattens a token or list of tokens into strings (for flag and label lists).
func tokenStrings(toks []token) []string {
	var out []string
	for _, t := range toks {
		if t.kind == tokList {
			out = append(out, tokenStrings(t.list)...)
		} else {
			out = append(out, t.s)
		}
	}
	return out
}

// quote renders s as an IMAP atom when safe, otherwise as a quoted string.
func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " ()\"\\{%*[]") && !strings.EqualFold(s, "NIL") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// nstring renders an optional string, NIL when empty.
func nstring(s string) string {
	if s == "" {
		return "NIL"
	}
	if strings.ContainsAny(s, "\r\n\"\\") || len(s) > 200 {
		return fmt.Sprintf("{%d}\r\n%s", len(s), s)
	}
	return `"` + s + `"`
}

// numSet is a parsed sequence set; * is resolved against max.
type numSet []numRange

type numRange struct{ lo, hi uint32 } // hi 0 means *

func parseNumSet(s string) (numSet, error) {
	var out numSet
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		a, err := parseNum(lo)
		if err != nil {
			return nil, err
		}
		b := a
		if isRange {
			if b, err = parseNum(hi); err != nil {
				return nil, err
			}
		}
		out = append(out, numRange{a, b})
	}
	return out, nil
}

func parseNum(s string) (uint32, error) {
	if s == "*" {
		return 0, nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("bad number %q", s)
	}
	return uint32(n), nil
}

// contains reports whether n is in the set, with * meaning max.
func (ns numSet) contains(n, max uint32) bool {
	for _, r := range ns {
		lo, hi := r.lo, r.hi
		if lo == 0 {
			lo = max
		}
		if hi == 0 {
			hi = max
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if n >= lo && n <= hi {
			return true
		}
	}
	return false
}
