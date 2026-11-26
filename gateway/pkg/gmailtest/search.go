package gmailtest

import (
	"bytes"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
)

type criterion func(tg target) bool

func (ss *session) search(p *parser, uid bool) error {
	toks, err := p.all()
	if err != nil {
		return err
	}
	if len(toks) >= 2 && toks[0].isAtom("CHARSET") {
		toks = toks[2:]
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if uid {
		ss.pushUpdatesLocked()
	}
	usesModSeq := false
	crit, rest, err := ss.parseCriteria(toks, &usesModSeq)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected search key %q", rest[0].s)
	}
	all, err := ss.targetsLocked("1:*", false)
	if err != nil {
		return err
	}
	var hits []string
	var highest uint64
	for _, tg := range all {
		if !crit(tg) {
			continue
		}
		if uid {
			hits = append(hits, strconv.Itoa(int(tg.uid)))
		} else {
			hits = append(hits, strconv.Itoa(tg.seq))
		}
		highest = max(highest, tg.m.ModSeq)
	}
	line := "* SEARCH"
	if len(hits) > 0 {
		line += " " + strings.Join(hits, " ")
		if usesModSeq {
			line += fmt.Sprintf(" (MODSEQ %d)", highest)
		}
	}
	ss.out("%s", line)
	return nil
}

// parseCriteria parses search keys until toks run out, ANDing them.
func (ss *session) parseCriteria(toks []token, usesModSeq *bool) (criterion, []token, error) {
	var crits []criterion
	for len(toks) > 0 {
		c, rest, err := ss.parseKey(toks, usesModSeq)
		if err != nil {
			return nil, nil, err
		}
		crits = append(crits, c)
		toks = rest
	}
	return andAll(crits), nil, nil
}

func andAll(crits []criterion) criterion {
	return func(tg target) bool {
		for _, c := range crits {
			if !c(tg) {
				return false
			}
		}
		return true
	}
}

func (ss *session) parseKey(toks []token, usesModSeq *bool) (criterion, []token, error) {
	t := toks[0]
	toks = toks[1:]
	if t.kind == tokList {
		c, _, err := ss.parseCriteria(t.list, usesModSeq)
		return c, toks, err
	}
	arg := func() (string, error) {
		if len(toks) == 0 {
			return "", fmt.Errorf("search key %s needs an argument", t.s)
		}
		a := toks[0].s
		toks = toks[1:]
		return a, nil
	}
	flag := func(f string, want bool) criterion {
		return func(tg target) bool { return tg.m.flags[f] == want }
	}
	key := strings.ToUpper(t.s)
	switch key {
	case "ALL":
		return func(target) bool { return true }, toks, nil
	case "SEEN", "UNSEEN":
		return flag(`\Seen`, key == "SEEN"), toks, nil
	case "FLAGGED", "UNFLAGGED":
		return flag(`\Flagged`, key == "FLAGGED"), toks, nil
	case "DELETED", "UNDELETED":
		return flag(`\Deleted`, key == "DELETED"), toks, nil
	case "DRAFT", "UNDRAFT":
		return flag(`\Draft`, key == "DRAFT"), toks, nil
	case "ANSWERED", "UNANSWERED":
		return flag(`\Answered`, key == "ANSWERED"), toks, nil
	case "KEYWORD", "UNKEYWORD":
		k, err := arg()
		return flag(k, key == "KEYWORD"), toks, err
	case "NOT":
		if len(toks) == 0 {
			return nil, nil, errors.New("NOT needs a key")
		}
		c, rest, err := ss.parseKey(toks, usesModSeq)
		if err != nil {
			return nil, nil, err
		}
		return func(tg target) bool { return !c(tg) }, rest, nil
	case "OR":
		if len(toks) == 0 {
			return nil, nil, errors.New("OR needs two keys")
		}
		a, rest, err := ss.parseKey(toks, usesModSeq)
		if err != nil || len(rest) == 0 {
			return nil, nil, errors.New("OR needs two keys")
		}
		b, rest, err := ss.parseKey(rest, usesModSeq)
		if err != nil {
			return nil, nil, err
		}
		return func(tg target) bool { return a(tg) || b(tg) }, rest, nil
	case "UID":
		s, err := arg()
		if err != nil {
			return nil, nil, err
		}
		ns, err := parseNumSet(s)
		if err != nil {
			return nil, nil, err
		}
		maxUID := uint32(0)
		if n := len(ss.sel.uids); n > 0 {
			maxUID = ss.sel.uids[n-1]
		}
		return func(tg target) bool { return ns.contains(tg.uid, maxUID) }, toks, nil
	case "MODSEQ":
		s, err := arg()
		if err != nil {
			return nil, nil, err
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, nil, errors.New("bad MODSEQ")
		}
		*usesModSeq = true
		ss.condstore = true
		return func(tg target) bool { return tg.m.ModSeq >= n }, toks, nil
	case "SUBJECT", "FROM", "TO", "CC", "BCC":
		s, err := arg()
		return headerContains(key, s), toks, err
	case "HEADER":
		name, err := arg()
		if err != nil {
			return nil, nil, err
		}
		s, err := arg()
		return headerContains(name, s), toks, err
	case "BODY", "TEXT":
		s, err := arg()
		return func(tg target) bool { return containsFold(tg.m.Raw, s) }, toks, err
	case "SINCE", "BEFORE", "ON":
		s, err := arg()
		if err != nil {
			return nil, nil, err
		}
		d, err := time.Parse("2-Jan-2006", s)
		if err != nil {
			return nil, nil, fmt.Errorf("bad date %q", s)
		}
		return func(tg target) bool {
			day := tg.m.InternalDate.UTC().Truncate(24 * time.Hour)
			switch key {
			case "SINCE":
				return !day.Before(d)
			case "BEFORE":
				return day.Before(d)
			}
			return day.Equal(d)
		}, toks, nil
	case "X-GM-MSGID", "X-GM-THRID":
		s, err := arg()
		if err != nil {
			return nil, nil, err
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("bad %s", key)
		}
		if key == "X-GM-MSGID" {
			return func(tg target) bool { return tg.m.GmMsgID == n }, toks, nil
		}
		return func(tg target) bool { return tg.m.GmThrID == n }, toks, nil
	case "X-GM-LABELS":
		s, err := arg()
		return func(tg target) bool { return slices.Contains(tg.m.Labels(), canonicalLabel(s)) }, toks, err
	case "X-GM-RAW":
		s, err := arg()
		if err != nil {
			return nil, nil, err
		}
		return gmRaw(s), toks, nil
	}
	if _, err := parseNumSet(t.s); err == nil { // a bare sequence set
		ns, _ := parseNumSet(t.s)
		n := uint32(len(ss.view))
		return func(tg target) bool { return ns.contains(uint32(tg.seq), n) }, toks, nil
	}
	return nil, nil, fmt.Errorf("unsupported search key %s", t.s)
}

func headerContains(name, s string) criterion {
	return func(tg target) bool {
		msg, err := mail.ReadMessage(bytes.NewReader(tg.m.Raw))
		if err != nil {
			return false
		}
		v := msg.Header.Get(name)
		if s == "" {
			return v != ""
		}
		return strings.Contains(strings.ToLower(v), strings.ToLower(s))
	}
}

func containsFold(b []byte, s string) bool {
	return bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(s)))
}

// gmRaw implements a small part of Gmail search syntax: space-separated terms, all
// of which must match; "-" negates. Keys: from: to: cc: subject: label: in: is:
// has:attachment rfc822msgid:; a bare word matches subject or body.
func gmRaw(q string) criterion {
	var crits []criterion
	for _, term := range splitTerms(q) {
		neg := strings.HasPrefix(term, "-")
		term = strings.TrimPrefix(term, "-")
		c := gmTerm(term)
		if neg {
			inner := c
			c = func(tg target) bool { return !inner(tg) }
		}
		crits = append(crits, c)
	}
	return andAll(crits)
}

func splitTerms(q string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func gmTerm(term string) criterion {
	key, val, ok := strings.Cut(term, ":")
	if !ok {
		return func(tg target) bool { return containsFold(tg.m.Raw, term) }
	}
	val = strings.ToLower(val)
	switch strings.ToLower(key) {
	case "from", "to", "cc", "subject":
		return headerContains(key, val)
	case "rfc822msgid":
		return func(tg target) bool {
			return strings.Trim(strings.ToLower(tg.m.msgID), "<>") == strings.Trim(val, "<>")
		}
	case "label", "in":
		return func(tg target) bool { return hasGmailLabel(tg.m, val) }
	case "is":
		return func(tg target) bool {
			switch val {
			case "unread":
				return !tg.m.flags[`\Seen`]
			case "read":
				return tg.m.flags[`\Seen`]
			case "starred":
				return tg.m.flags[`\Flagged`]
			case "important":
				return tg.m.labels[LabelImportant]
			}
			return false
		}
	case "has":
		return func(tg target) bool {
			return val == "attachment" && containsFold(tg.m.Raw, "content-disposition: attachment")
		}
	}
	return func(target) bool { return false }
}

// hasGmailLabel matches label:x / in:x. Gmail search writes spaces and slashes in
// label names as "-".
func hasGmailLabel(m *Message, val string) bool {
	switch val {
	case "anywhere":
		return true
	case "inbox":
		return m.labels[LabelInbox]
	case "sent":
		return m.labels[LabelSent]
	case "drafts", "draft":
		return m.flags[`\Draft`]
	case "trash":
		return m.labels[LabelTrash]
	case "spam":
		return m.labels[LabelSpam]
	case "starred":
		return m.flags[`\Flagged`]
	case "important":
		return m.labels[LabelImportant]
	}
	norm := func(s string) string { return strings.NewReplacer(" ", "-", "/", "-").Replace(strings.ToLower(s)) }
	for l := range m.labels {
		if norm(l) == norm(val) {
			return true
		}
	}
	return false
}
