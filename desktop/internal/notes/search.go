package notes

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const snippetRadius = 40

// matchSnippet returns one line of text around the first match; query is already lower-cased.
func matchSnippet(content, query string) string {
	lower := strings.ToLower(content)
	at := strings.Index(lower, query)
	// ToLower can change byte lengths for some scripts; only cut when offsets still line up.
	if at < 0 || len(lower) != len(content) {
		return ""
	}
	start := at
	for n := 0; start > 0 && n < snippetRadius; n++ {
		_, size := utf8.DecodeLastRuneInString(content[:start])
		start -= size
	}
	end := at + len(query)
	for n := 0; end < len(content) && n < snippetRadius*2; n++ {
		_, size := utf8.DecodeRuneInString(content[end:])
		end += size
	}
	text := strings.Join(strings.Fields(content[start:end]), " ")
	if start > 0 {
		text = "…" + text
	}
	if end < len(content) {
		text += "…"
	}
	return text
}

// folderPath joins the names of a live entry's parent folders, root first; callers hold s.mu.
func (s *Service) folderPath(e Entry) string {
	parts := []string{}
	for parent, depth := e.ParentID, 0; parent != "" && depth <= 64; depth++ {
		p, ok := s.entries[parent]
		if !ok {
			break
		}
		parts = append([]string{p.Name}, parts...)
		parent = p.ParentID
	}
	return strings.Join(parts, "/")
}

// freeName picks "name", "name 2", "name 3"… among live siblings; callers hold s.mu.
func (s *Service) freeName(vault, parent, name string, directory bool) string {
	stem, ext := name, ""
	if !directory {
		stem, ext = strings.TrimSuffix(name, extension(name)), extension(name)
	}
	candidate := name
	for n := 2; n < 1000 && s.unique(vault, parent, candidate, "") != nil; n++ {
		candidate = fmt.Sprintf("%s %d%s", stem, n, ext)
	}
	return candidate
}
