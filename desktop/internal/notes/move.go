package notes

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

var escapedMoveName = regexp.MustCompile(`(?:%[0-9a-fA-F]{2})+`)

// moveReferenceText normalizes encoded or Markdown-escaped names for conservative reference checks.
func moveReferenceText(content string) string {
	decoded := escapedMoveName.ReplaceAllStringFunc(html.UnescapeString(content), func(encoded string) string {
		value, _ := url.PathUnescape(encoded)
		return value
	})
	return strings.ReplaceAll(decoded, `\`, "")
}

// hasMoveLinks recognizes potential Markdown destinations without interpreting code as safe prose.
func hasMoveLinks(content string) bool {
	return strings.Contains(content, "](") || strings.Contains(content, "[[") || strings.Contains(content, "]:")
}

// safeMove allows whole subtrees without rewriting any note; ambiguous references remain fail-closed.
// The caller holds s.mu. Stable parent IDs keep all descendants attached to the moved directory.
func (s *Service) safeMove(root Entry, parent, name string) error {
	if root.ParentID == parent && root.Name == name {
		return nil
	}
	subtree := map[string]Entry{root.ID: root}
	if root.Directory {
		for _, e := range s.entries {
			if e.VaultID != root.VaultID {
				continue
			}
			id := e.ParentID
			for depth := 0; id != "" && depth <= 64; depth++ {
				if id == root.ID {
					subtree[e.ID] = e
					break
				}
				ancestor, ok := s.entries[id]
				if !ok || ancestor.VaultID != root.VaultID {
					break
				}
				id = ancestor.ParentID
			}
		}
	}
	for _, e := range subtree {
		if e.Status == "conflict" || e.Uncertain {
			return ErrConflict
		}
		// Moving a directory can change every descendant's document-relative links.
		// Until parsed reference rewriting is available, reject all potential link syntaxes.
		if !e.Directory && !e.Attachment && (parent != root.ParentID || root.Directory && name != root.Name) && hasMoveLinks(e.Content) {
			return ErrConflict
		}
	}
	for _, other := range s.entries {
		if other.VaultID != root.VaultID || other.Directory || other.Attachment {
			continue
		}
		if _, inside := subtree[other.ID]; inside && root.Directory {
			continue
		}
		content := moveReferenceText(other.Content)
		if !hasMoveLinks(content) {
			continue
		}
		for _, moved := range subtree {
			stem := moved.Name
			if !moved.Directory && !moved.Attachment && strings.HasSuffix(strings.ToLower(stem), ".md") {
				stem = stem[:len(stem)-3]
			}
			if strings.Contains(content, moved.Name) || stem != "" && strings.Contains(content, stem) {
				return ErrConflict
			}
		}
	}
	return nil
}
