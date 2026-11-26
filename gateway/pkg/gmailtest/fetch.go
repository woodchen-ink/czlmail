package gmailtest

import (
	"bytes"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// target is one message addressed by a command.
type target struct {
	seq int
	uid uint32
	m   *Message
}

// targetsLocked resolves a sequence or UID set against the client's view.
func (ss *session) targetsLocked(set string, uid bool) ([]target, error) {
	ns, err := parseNumSet(set)
	if err != nil {
		return nil, err
	}
	f := ss.sel
	var out []target
	var maxUID uint32
	if len(f.uids) > 0 {
		maxUID = f.uids[len(f.uids)-1]
	}
	for i, u := range ss.view {
		m, ok := f.byUID[u]
		if !ok {
			continue
		}
		if (uid && ns.contains(u, maxUID)) || (!uid && ns.contains(uint32(i+1), uint32(len(ss.view)))) {
			out = append(out, target{seq: i + 1, uid: u, m: m})
		}
	}
	return out, nil
}

func (ss *session) fetch(p *parser, uid bool) error {
	set, err := p.str()
	if err != nil {
		return err
	}
	itemsTok, err := p.next()
	if err != nil {
		return err
	}
	items := []token{itemsTok}
	if itemsTok.kind == tokList {
		items = itemsTok.list
	}
	var names []string
	for _, it := range items {
		switch strings.ToUpper(it.s) {
		case "FAST":
			names = append(names, "FLAGS", "INTERNALDATE", "RFC822.SIZE")
		case "ALL":
			names = append(names, "FLAGS", "INTERNALDATE", "RFC822.SIZE", "ENVELOPE")
		default:
			names = append(names, it.s)
		}
	}
	var changedSince uint64
	if !p.done() {
		mods, err := p.next()
		if err != nil || mods.kind != tokList || len(mods.list) != 2 || !mods.list[0].isAtom("CHANGEDSINCE") {
			return errors.New("bad FETCH modifiers")
		}
		if changedSince, err = strconv.ParseUint(mods.list[1].s, 10, 64); err != nil {
			return errors.New("bad CHANGEDSINCE")
		}
		ss.condstore = true
	}
	if uid && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, "UID") }) {
		names = append([]string{"UID"}, names...)
	}
	if changedSince > 0 && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, "MODSEQ") }) {
		names = append(names, "MODSEQ")
	}

	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if uid {
		ss.pushUpdatesLocked()
	}
	targets, err := ss.targetsLocked(set, uid)
	if err != nil {
		return err
	}
	seen := false
	for _, tg := range targets {
		if changedSince > 0 && tg.m.ModSeq <= changedSince {
			continue
		}
		var parts []string
		setSeen := false
		for _, n := range names {
			s, marks, err := ss.fetchItem(n, tg)
			if err != nil {
				return err
			}
			parts = append(parts, s)
			setSeen = setSeen || marks
		}
		if setSeen && !ss.readOnly && !tg.m.flags[`\Seen`] {
			tg.m.flags[`\Seen`] = true
			ss.s.touchLocked(tg.m)
			seen = true
			parts = append(parts, fmt.Sprintf("FLAGS (%s)", strings.Join(tg.m.Flags(), " ")))
		}
		ss.out("* %d FETCH (%s)", tg.seq, strings.Join(parts, " "))
	}
	if seen {
		ss.s.syncLocked()
		ss.seenMod = ss.s.st.modseq
	}
	return nil
}

var sectionRe = regexp.MustCompile(`(?i)^(BODY|BODY\.PEEK|BINARY|BINARY\.PEEK)\[([^\]]*)\](?:<(\d+)(?:\.(\d+))?>)?$`)

// fetchItem renders one FETCH item; marksSeen is true for non-PEEK body fetches.
func (ss *session) fetchItem(name string, tg target) (string, bool, error) {
	m := tg.m
	switch up := strings.ToUpper(name); up {
	case "UID":
		return fmt.Sprintf("UID %d", tg.uid), false, nil
	case "FLAGS":
		return fmt.Sprintf("FLAGS (%s)", strings.Join(m.Flags(), " ")), false, nil
	case "INTERNALDATE":
		return fmt.Sprintf(`INTERNALDATE "%s"`, m.InternalDate.Format("02-Jan-2006 15:04:05 -0700")), false, nil
	case "RFC822.SIZE":
		return fmt.Sprintf("RFC822.SIZE %d", len(m.Raw)), false, nil
	case "MODSEQ":
		return fmt.Sprintf("MODSEQ (%d)", m.ModSeq), false, nil
	case "X-GM-MSGID":
		return fmt.Sprintf("X-GM-MSGID %d", m.GmMsgID), false, nil
	case "X-GM-THRID":
		return fmt.Sprintf("X-GM-THRID %d", m.GmThrID), false, nil
	case "X-GM-LABELS":
		return fmt.Sprintf("X-GM-LABELS (%s)", labelList(m)), false, nil
	case "EMAILID", "THREADID":
		if !ss.s.opts.ObjectID {
			return "", false, fmt.Errorf("unknown fetch item %s", name)
		}
		if up == "EMAILID" {
			return fmt.Sprintf("EMAILID (M%x)", m.GmMsgID), false, nil
		}
		return fmt.Sprintf("THREADID (T%x)", m.GmThrID), false, nil
	case "ENVELOPE":
		return "ENVELOPE " + envelope(m.Raw), false, nil
	case "RFC822":
		return literalItem("RFC822", m.Raw), true, nil
	case "RFC822.HEADER":
		h, _ := splitMessage(m.Raw)
		return literalItem("RFC822.HEADER", h), false, nil
	case "RFC822.TEXT":
		_, b := splitMessage(m.Raw)
		return literalItem("RFC822.TEXT", b), true, nil
	}
	sm := sectionRe.FindStringSubmatch(name)
	if sm == nil {
		return "", false, fmt.Errorf("unsupported fetch item %s", name)
	}
	kind, section := strings.ToUpper(sm[1]), sm[2]
	data, err := sectionData(m.Raw, section)
	if err != nil {
		return "", false, err
	}
	label := strings.TrimSuffix(kind, ".PEEK") + "[" + section + "]"
	if sm[3] != "" {
		start, _ := strconv.Atoi(sm[3])
		start = min(start, len(data))
		data = data[start:]
		if sm[4] != "" {
			n, _ := strconv.Atoi(sm[4])
			data = data[:min(n, len(data))]
		}
		label += "<" + sm[3] + ">"
	}
	return literalItem(label, data), !strings.HasSuffix(kind, ".PEEK"), nil
}

func literalItem(name string, data []byte) string {
	return fmt.Sprintf("%s {%d}\r\n%s", name, len(data), data)
}

func splitMessage(raw []byte) (header, body []byte) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i+4], raw[i+4:]
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i+2], raw[i+2:]
	}
	return raw, nil
}

func sectionData(raw []byte, section string) ([]byte, error) {
	header, body := splitMessage(raw)
	up := strings.ToUpper(strings.TrimSpace(section))
	switch {
	case up == "":
		return raw, nil
	case up == "HEADER":
		return header, nil
	case up == "TEXT", up == "1":
		return body, nil
	case strings.HasPrefix(up, "HEADER.FIELDS"):
		not := strings.HasPrefix(up, "HEADER.FIELDS.NOT")
		open, close := strings.Index(up, "("), strings.LastIndex(up, ")")
		if open < 0 || close < open {
			return nil, errors.New("bad HEADER.FIELDS")
		}
		want := map[string]bool{}
		for _, f := range strings.Fields(up[open+1 : close]) {
			want[strings.Trim(f, `"`)] = true
		}
		var out bytes.Buffer
		for _, field := range headerFields(header) {
			name, _, _ := strings.Cut(field, ":")
			if want[strings.ToUpper(strings.TrimSpace(name))] != not {
				out.WriteString(field)
			}
		}
		out.WriteString("\r\n")
		return out.Bytes(), nil
	}
	return nil, fmt.Errorf("unsupported body section %q", section)
}

// headerFields splits a header block into fields, keeping folded lines with their field.
func headerFields(header []byte) []string {
	var out []string
	for _, line := range strings.SplitAfter(string(header), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += line
			continue
		}
		out = append(out, line)
	}
	return out
}

func envelope(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "(NIL NIL NIL NIL NIL NIL NIL NIL NIL NIL)"
	}
	h := msg.Header
	from := addrList(h, "From")
	sender, replyTo := addrList(h, "Sender"), addrList(h, "Reply-To")
	if sender == "NIL" {
		sender = from
	}
	if replyTo == "NIL" {
		replyTo = from
	}
	return fmt.Sprintf("(%s %s %s %s %s %s %s %s %s %s)",
		nstring(h.Get("Date")), nstring(h.Get("Subject")), from, sender, replyTo,
		addrList(h, "To"), addrList(h, "Cc"), addrList(h, "Bcc"),
		nstring(h.Get("In-Reply-To")), nstring(h.Get("Message-Id")))
}

func addrList(h mail.Header, key string) string {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		return "NIL"
	}
	var b strings.Builder
	b.WriteString("(")
	for _, a := range list {
		local, host, _ := strings.Cut(a.Address, "@")
		fmt.Fprintf(&b, "(%s NIL %s %s)", nstring(a.Name), nstring(local), nstring(host))
	}
	b.WriteString(")")
	return b.String()
}
