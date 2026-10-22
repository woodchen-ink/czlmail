package notes

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Vaults lists configured libraries without creating or connecting anything.
func (s *Service) Vaults() ([]Vault, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return nil, ErrDisabled
	}
	out := []Vault{}
	for _, v := range s.vaults {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Bind connects an account to an existing directory, or explicitly creates/reuses Notes.
func (s *Service) Bind(ctx context.Context, account, rootID string) (Vault, error) {
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return Vault{}, ErrDisabled
	}
	r := s.remote
	s.mu.Unlock()
	if r == nil || account == "" {
		return Vault{}, ErrUnavailable
	}
	snap, err := r.Snapshot(ctx, account)
	if err != nil {
		return Vault{}, err
	}
	var root Node
	for _, n := range snap.Nodes {
		if rootID != "" && n.ID == rootID || rootID == "" && n.ParentID == "" && n.Name == "Notes" {
			root = n
			break
		}
	}
	if root.ID == "" && rootID == "" {
		root, err = r.Set(ctx, account, snap.State, "", Node{Name: "Notes", Directory: true})
		if err != nil {
			return Vault{}, err
		}
	}
	if root.ID == "" || !root.Directory || root.Name != "Notes" || root.ParentID != "" {
		return Vault{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return Vault{}, ErrDisabled
	}
	for _, v := range s.vaults {
		if v.AccountID == account {
			if v.RootID == root.ID {
				return v, nil
			}
			return Vault{}, ErrInvalid
		}
	}
	v := Vault{ID: newID(), AccountID: account, RootID: root.ID, Name: root.Name}
	if err = s.put("vault", v.ID, v); err != nil {
		return Vault{}, err
	}
	s.vaults[v.ID] = v
	s.notify()
	return v, nil
}

// List returns sorted metadata or literal substring search results in one library.
func (s *Service) List(vault, parent, query string, trash bool) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return nil, ErrDisabled
	}
	if _, ok := s.vaults[vault]; !ok {
		return nil, ErrNotFound
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []Entry{}
	for _, e := range s.entries {
		if e.VaultID != vault || e.Name == ".czl-trash" || (trash && (!e.Deleted || !s.trashRoot(e))) || (!trash && s.effectivelyDeleted(e)) {
			continue
		}
		if query == "" && e.ParentID != parent && !trash {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(e.Name+"\n"+e.Content), query) {
			continue
		}
		snippet := ""
		if query != "" {
			snippet = matchSnippet(e.Content, query)
		}
		e = clone(e)
		e.Content = ""
		e.RemoteContent = ""
		e.Snippet = snippet
		if query != "" || trash {
			e.Path = s.folderPath(e)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		// Search results read like mail search: the most recently edited match first.
		if query != "" {
			return out[i].Modified > out[j].Modified
		}
		if out[i].Directory != out[j].Directory {
			return out[i].Directory
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Read returns the latest local version, not merely the last server version.
func (s *Service) Read(vault, id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(vault, id)
	if err == nil {
		e.Deleted = s.effectivelyDeleted(e)
		e.Path = s.folderPath(e)
	}
	return e, err
}

// entry checks module, library and object boundaries while holding the lock.
func (s *Service) entry(vault, id string) (Entry, error) {
	if !s.config.Enabled {
		return Entry{}, ErrDisabled
	}
	e, ok := s.entries[id]
	if !ok || e.VaultID != vault {
		return Entry{}, ErrNotFound
	}
	return clone(e), nil
}

// parent validates a live directory in the same library and rejects cycles.
func (s *Service) parent(vault, parent, id string) error {
	for depth := 0; parent != ""; depth++ {
		if depth > 64 || parent == id {
			return ErrInvalid
		}
		e, err := s.entry(vault, parent)
		if err != nil || !e.Directory || s.effectivelyDeleted(e) {
			return ErrInvalid
		}
		parent = e.ParentID
	}
	return nil
}

// unique rejects case-insensitive collisions without silently renaming existing files.
func (s *Service) unique(vault, parent, name, id string) error {
	for _, e := range s.entries {
		if e.ID != id && e.VaultID == vault && e.ParentID == parent && strings.EqualFold(e.Name, name) && !e.Deleted {
			return ErrInvalid
		}
	}
	return nil
}

// Create durably accepts one Markdown document or directory before any upload.
func (s *Service) Create(in Edit, directory bool) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return Entry{}, ErrDisabled
	}
	if _, ok := s.vaults[in.VaultID]; !ok {
		return Entry{}, ErrNotFound
	}
	hash := digest(struct {
		Edit      Edit
		Directory bool
	}{in, directory})
	if e, ok, err := s.retry(in.IdempotencyKey, hash); ok || err != nil {
		return e, err
	}
	name := in.Name
	if !directory && !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	if !validName(name) || len(in.Content) > MaxText || !utf8.ValidString(in.Content) {
		return Entry{}, ErrInvalid
	}
	if err := s.parent(in.VaultID, in.ParentID, ""); err != nil {
		return Entry{}, err
	}
	if in.Unique {
		name = s.freeName(in.VaultID, in.ParentID, name, directory)
	}
	if err := s.unique(in.VaultID, in.ParentID, name, ""); err != nil {
		return Entry{}, err
	}
	e := Entry{ID: newID(), VaultID: in.VaultID, ParentID: in.ParentID, Name: name, Directory: directory, Content: in.Content, Status: "pending", Type: "text/markdown"}
	stamp(&e)
	return e, s.commit(e, in.IdempotencyKey, hash)
}

// Update applies a local compare-and-swap; stale UI or MCP buffers cannot overwrite it.
func (s *Service) Update(in Edit) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(in.VaultID, in.ID)
	if err != nil {
		return Entry{}, err
	}
	hash := digest(in)
	if old, ok, err := s.retry(in.IdempotencyKey, hash); ok || err != nil {
		return old, err
	}
	if e.Revision != in.ExpectedRevision {
		return Entry{}, ErrRevision
	}
	if e.Directory || e.Attachment || s.effectivelyDeleted(e) || len(in.Content) > MaxText || !utf8.ValidString(in.Content) {
		return Entry{}, ErrInvalid
	}
	if e.Content == in.Content {
		return e, nil
	}
	e.Content = in.Content
	if e.Status != "conflict" {
		e.Status = "pending"
	}
	stamp(&e)
	return e, s.commit(e, in.IdempotencyKey, hash)
}

// Move changes only metadata; Markdown links and other notes' references remain untouched.
// Revision, directory, name and synchronization-state checks still apply.
func (s *Service) Move(in Edit) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(in.VaultID, in.ID)
	if err != nil {
		return Entry{}, err
	}
	if e.Revision != in.ExpectedRevision {
		return Entry{}, ErrRevision
	}
	if s.effectivelyDeleted(e) || e.Status == "conflict" || e.Uncertain {
		return Entry{}, ErrConflict
	}
	if !validName(in.Name) {
		return Entry{}, ErrInvalid
	}
	if err = s.parent(in.VaultID, in.ParentID, e.ID); err != nil {
		return Entry{}, err
	}
	if err = s.unique(in.VaultID, in.ParentID, in.Name, e.ID); err != nil {
		return Entry{}, err
	}
	if err = s.safeMove(e); err != nil {
		return Entry{}, err
	}
	e.Name = in.Name
	e.ParentID = in.ParentID
	e.Status = "pending"
	stamp(&e)
	return e, s.commit(e, in.IdempotencyKey, digest(in))
}

// Trash moves a document into a retained remote trash directory; attachments remain intact.
func (s *Service) Trash(vault, id string, revision int64, restore bool) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(vault, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Revision != revision {
		return Entry{}, ErrRevision
	}
	if e.Status == "conflict" || e.Uncertain {
		return Entry{}, ErrConflict
	}
	if restore {
		if !e.Deleted {
			return e, nil
		}
		if err = s.parent(vault, e.OriginalParent, e.ID); err != nil {
			return Entry{}, err
		}
		if err = s.unique(vault, e.OriginalParent, e.Name, e.ID); err != nil {
			return Entry{}, err
		}
		e.ParentID = e.OriginalParent
		e.Deleted = false
	} else {
		if e.Deleted {
			return e, nil
		}
		e.OriginalParent = e.ParentID
		e.Deleted = true
	}
	e.Status = "pending"
	stamp(&e)
	return e, s.commit(e, "", "")
}

// saveEntry checkpoints remote reconciliation, preserving local versions on errors.
func (s *Service) saveEntry(e Entry) error {
	if err := s.put("entry", e.ID, e); err != nil {
		return err
	}
	s.entries[e.ID] = e
	s.notify()
	return nil
}

// nowUTC records sync time for display, never for conflict resolution.
func nowUTC() int64 { return time.Now().UnixMilli() }
