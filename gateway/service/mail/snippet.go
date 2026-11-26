package mail

import (
	"html"
	"strings"
)

// highlight HTML-escapes s and wraps every occurrence of a term in <mark>
// (RFC 8621 §5.1). It returns nil when no term occurs.
func highlight(s string, terms []string) any {
	if s == "" || len(terms) == 0 {
		return nil
	}
	lower, exact := strings.ToLower(s), false
	if len(lower) != len(s) {
		lower, exact = s, true // case mapping changed byte lengths: match exactly so offsets line up
	}
	type span struct{ from, to int }
	var spans []span
	for _, t := range terms {
		lt := strings.TrimSpace(t)
		if !exact {
			lt = strings.ToLower(lt)
		}
		for i := 0; lt != ""; {
			j := strings.Index(lower[i:], lt)
			if j < 0 {
				break
			}
			spans = append(spans, span{i + j, i + j + len(lt)})
			i += j + len(lt)
		}
	}
	if len(spans) == 0 {
		return nil
	}
	// Merge overlapping spans, in order.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].from < spans[j-1].from; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var b strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp.from < pos {
			if sp.to <= pos {
				continue
			}
			sp.from = pos
		}
		if sp.to > len(s) {
			break
		}
		b.WriteString(html.EscapeString(s[pos:sp.from]))
		b.WriteString("<mark>")
		b.WriteString(html.EscapeString(s[sp.from:sp.to]))
		b.WriteString("</mark>")
		pos = sp.to
	}
	b.WriteString(html.EscapeString(s[pos:]))
	return b.String()
}
