package imap

import (
	"slices"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
)

// specialUse maps RFC 6154 attributes to JMAP roles.
var specialUse = map[string]string{
	`\all`:       "all",
	`\archive`:   "archive",
	`\drafts`:    "drafts",
	`\flagged`:   "flagged",
	`\junk`:      "junk",
	`\sent`:      "sent",
	`\trash`:     "trash",
	`\important`: "important",
}

// roleNames are common folder names when the server has no SPECIAL-USE, matched on
// the last path segment, lower-cased.
var roleNames = map[string]string{
	"sent": "sent", "sent items": "sent", "sent messages": "sent", "sent mail": "sent", "已发送": "sent", "已发送邮件": "sent", "gesendet": "sent", "envoyés": "sent",
	"drafts": "drafts", "draft": "drafts", "草稿": "drafts", "草稿箱": "drafts", "entwürfe": "drafts", "brouillons": "drafts",
	"trash": "trash", "deleted": "trash", "deleted items": "trash", "deleted messages": "trash", "bin": "trash", "已删除": "trash", "已删除邮件": "trash", "papierkorb": "trash", "corbeille": "trash",
	"junk": "junk", "spam": "junk", "junk e-mail": "junk", "junk email": "junk", "垃圾邮件": "junk", "垃圾箱": "junk",
	"archive": "archive", "archives": "archive", "归档": "archive", "存档": "archive", "archiv": "archive",
}

// Strength of a role guess, strongest first.
const (
	roleByAttr = iota
	roleByTopName
	roleByNestedName
)

// assignRoles gives each folder of a LIST its JMAP role, at most one folder per
// role (RFC 8621 2): SPECIAL-USE attributes claim first, then top-level folder
// names, then nested ones (a user's "Inbox/Archive" next to Exchange's "Archive"),
// each in LIST order.
func assignRoles(list []*imapc.ListEntry) []string {
	type guess struct{ i, strength int }
	var guesses []guess
	cand := make([]string, len(list))
	for i, e := range list {
		r, strength := roleOf(e)
		if r != "" {
			cand[i] = r
			guesses = append(guesses, guess{i, strength})
		}
	}
	slices.SortStableFunc(guesses, func(a, b guess) int { return a.strength - b.strength })
	out := make([]string, len(list))
	taken := map[string]bool{}
	for _, g := range guesses {
		if r := cand[g.i]; !taken[r] {
			taken[r], out[g.i] = true, r
		}
	}
	return out
}

// roleOf guesses the JMAP role of one folder: SPECIAL-USE first, then the name.
func roleOf(e *imapc.ListEntry) (string, int) {
	if strings.EqualFold(e.Name, "INBOX") {
		return "inbox", roleByAttr
	}
	for _, a := range e.Attrs {
		if r, ok := specialUse[strings.ToLower(a)]; ok {
			return r, roleByAttr
		}
	}
	name, strength := e.Name, roleByTopName
	if e.Delimiter != "" {
		if i := strings.LastIndex(name, e.Delimiter); i >= 0 {
			name, strength = name[i+len(e.Delimiter):], roleByNestedName
		}
	}
	if r := roleNames[strings.ToLower(name)]; r != "" {
		return r, strength
	}
	return "", 0
}

// sortOrder puts the usual folders first, as clients expect.
func sortOrder(role string) int {
	switch role {
	case "inbox":
		return 1
	case "drafts":
		return 2
	case "sent":
		return 3
	case "archive":
		return 4
	case "junk":
		return 5
	case "trash":
		return 6
	}
	return 10
}

// parentName is the name of the folder containing name, or "".
func parentName(name, delim string) string {
	if delim == "" {
		return ""
	}
	i := strings.LastIndex(name, delim)
	if i <= 0 {
		return ""
	}
	return name[:i]
}

func noSelect(e *imapc.ListEntry) bool {
	return e.HasAttr(`\Noselect`) || e.HasAttr(`\NonExistent`)
}
