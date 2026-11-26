package gmailtest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (ss *session) store(p *parser, uid bool) error {
	if ss.readOnly {
		return no("[READ-ONLY] folder is read-only")
	}
	set, err := p.str()
	if err != nil {
		return err
	}
	t, err := p.next()
	if err != nil {
		return err
	}
	var unchangedSince uint64
	hasUnchanged := false
	if t.kind == tokList {
		if len(t.list) != 2 || !t.list[0].isAtom("UNCHANGEDSINCE") {
			return errors.New("bad STORE modifiers")
		}
		if unchangedSince, err = strconv.ParseUint(t.list[1].s, 10, 64); err != nil {
			return errors.New("bad UNCHANGEDSINCE")
		}
		hasUnchanged = true
		ss.condstore = true
		if t, err = p.next(); err != nil {
			return err
		}
	}
	item := strings.ToUpper(t.s)
	op := byte('=')
	if item != "" && (item[0] == '+' || item[0] == '-') {
		op, item = item[0], item[1:]
	}
	silent := strings.HasSuffix(item, ".SILENT")
	item = strings.TrimSuffix(item, ".SILENT")
	if item != "FLAGS" && item != "X-GM-LABELS" {
		return fmt.Errorf("unsupported STORE item %s", t.s)
	}
	vals, err := p.all()
	if err != nil {
		return err
	}
	values := tokenStrings(vals)

	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if uid {
		ss.pushUpdatesLocked()
	}
	targets, err := ss.targetsLocked(set, uid)
	if err != nil {
		return err
	}
	var modified []string
	var changed []target
	for _, tg := range targets {
		m := tg.m
		if hasUnchanged && m.ModSeq > unchangedSince {
			if uid {
				modified = append(modified, strconv.Itoa(int(tg.uid)))
			} else {
				modified = append(modified, strconv.Itoa(tg.seq))
			}
			continue
		}
		if item == "FLAGS" {
			applyFlags(m, op, values)
		} else {
			if op == '=' {
				clear(m.labels)
				delete(m.flags, `\Flagged`)
				delete(m.flags, `\Draft`)
			}
			for _, l := range values {
				setLabel(m, l, op != '-')
				if op != '-' {
					ss.s.ensureLabelFolderLocked(canonicalLabel(l))
				}
			}
		}
		ss.s.touchLocked(m)
		changed = append(changed, tg)
	}
	ss.s.syncLocked()
	for _, tg := range changed {
		if !silent || ss.condstore {
			items := fmt.Sprintf("FLAGS (%s)", strings.Join(tg.m.Flags(), " "))
			if item == "X-GM-LABELS" {
				items = fmt.Sprintf("X-GM-LABELS (%s)", labelList(tg.m))
			}
			if uid {
				items = fmt.Sprintf("UID %d %s", tg.uid, items)
			}
			if ss.condstore {
				items += fmt.Sprintf(" MODSEQ (%d)", tg.m.ModSeq)
			}
			ss.out("* %d FETCH (%s)", tg.seq, items)
		}
	}
	ss.seenMod = ss.s.st.modseq
	if len(modified) > 0 {
		ss.out("%s OK [MODIFIED %s] Conditional STORE failed", ss.tag, strings.Join(modified, ","))
		return errAnswered
	}
	return nil
}

func applyFlags(m *Message, op byte, flags []string) {
	if op == '=' {
		clear(m.flags)
	}
	for _, f := range flags {
		if strings.EqualFold(f, `\Recent`) {
			continue
		}
		setFlag(m, canonicalFlag(f), op != '-')
	}
}

func (ss *session) copyMove(p *parser, uid, move bool) error {
	set, err1 := p.str()
	dest, err2 := p.str()
	if err1 != nil || err2 != nil {
		return errors.New("COPY/MOVE needs a set and a folder")
	}
	if move && ss.readOnly {
		return no("[READ-ONLY] folder is read-only")
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if uid {
		ss.pushUpdatesLocked()
	}
	df := ss.s.folderLocked(dest)
	if df == nil || df.match == nil {
		return no("[TRYCREATE] Folder doesn't exist. (Failure)")
	}
	targets, err := ss.targetsLocked(set, uid)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	for _, tg := range targets {
		df.add(tg.m)
		ss.s.touchLocked(tg.m)
	}
	ss.s.syncLocked()
	var src, dst []string
	for _, tg := range targets {
		if du := df.uidOf[tg.m]; du != 0 {
			src = append(src, strconv.Itoa(int(tg.uid)))
			dst = append(dst, strconv.Itoa(int(du)))
		}
	}
	copyUID := fmt.Sprintf("[COPYUID %d %s %s]", df.uidv, strings.Join(src, ","), strings.Join(dst, ","))
	if !move {
		ss.out("%s OK %s (Success)", ss.tag, copyUID)
		return errAnswered
	}
	// All Mail is every message: moving out of it only adds the destination label.
	if ss.sel.name != AllMail && df != ss.sel {
		for _, tg := range targets {
			ss.sel.remove(ss.s, tg.m)
			ss.s.touchLocked(tg.m)
		}
		ss.s.syncLocked()
	}
	ss.out("* OK %s", copyUID)
	ss.pushUpdatesLocked()
	return nil
}

// expunge removes \Deleted messages (limited to uids when non-nil) from the selected
// folder, with Gmail's meaning: drop the label, or trash / delete for the system folders.
func (ss *session) expunge(uids numSet) {
	if ss.readOnly {
		return
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	f := ss.sel
	var maxUID uint32
	if len(f.uids) > 0 {
		maxUID = f.uids[len(f.uids)-1]
	}
	for _, u := range f.uids {
		m := f.byUID[u]
		if !m.flags[`\Deleted`] || (uids != nil && !uids.contains(u, maxUID)) {
			continue
		}
		delete(m.flags, `\Deleted`)
		f.remove(ss.s, m)
		ss.s.touchLocked(m)
	}
	ss.s.syncLocked()
	ss.pushUpdatesLocked()
}
