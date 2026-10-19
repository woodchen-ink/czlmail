package notes

import (
	"net/url"
	"path"
	"strings"
)

// ResolveLink finds a library-relative document or attachment, including unambiguous imported wiki names.
func (s *Service) ResolveLink(vault, noteID, relative string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveLink(vault, noteID, relative)
}

// resolveLink never resolves outside the configured root, even through percent-encoded dot segments.
func (s *Service) resolveLink(vault, noteID, relative string) (Entry, error) {
	note, err := s.entry(vault, noteID)
	if err != nil {
		return Entry{}, err
	}
	relative, _, _ = strings.Cut(relative, "#")
	decoded, err := url.PathUnescape(relative)
	if err != nil || decoded == "" || strings.ContainsAny(decoded, "\\\x00:") || strings.HasPrefix(decoded, "/") {
		return Entry{}, ErrInvalid
	}
	parent := note.ParentID
	var target Entry
	for _, part := range strings.Split(decoded, "/") {
		if part == "." || part == "" {
			continue
		}
		if part == ".." {
			if parent == "" {
				return Entry{}, ErrInvalid
			}
			parent = s.entries[parent].ParentID
			target = Entry{}
			continue
		}
		found := false
		for _, e := range s.entries {
			if e.VaultID == vault && e.ParentID == parent && e.Name == part && !e.Deleted {
				target = clone(e)
				found = true
				break
			}
		}
		if !found {
			target = Entry{}
			break
		}
		parent = target.ID
	}
	if target.ID != "" {
		return target, nil
	}
	// Existing Obsidian embeds often use a bare filename. Only a unique basename may resolve.
	if !strings.Contains(decoded, "/") {
		for _, e := range s.entries {
			if e.VaultID != vault || e.Deleted {
				continue
			}
			if e.Name == decoded || !e.Attachment && e.Name == decoded+".md" {
				if target.ID != "" {
					return Entry{}, ErrConflict
				}
				target = clone(e)
			}
		}
		if target.ID != "" {
			return target, nil
		}
	}
	// A vault-root-relative Obsidian link may differ from a standard document-relative link.
	parent = ""
	for _, part := range strings.Split(path.Clean(decoded), "/") {
		if part == ".." || part == "." || part == "" {
			return Entry{}, ErrInvalid
		}
		found := false
		for _, e := range s.entries {
			if e.VaultID == vault && e.ParentID == parent && (e.Name == part || e.Name == part+".md") && !e.Deleted {
				target = clone(e)
				found = true
				break
			}
		}
		if !found {
			return Entry{}, ErrNotFound
		}
		parent = target.ID
	}
	if target.ID == "" {
		return Entry{}, ErrNotFound
	}
	return target, nil
}
