package notes

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// Sync reconciles full remote metadata and changed bodies, then submits guarded local work.
// A single worker runs at a time; network requests never hold the local editing mutex.
func (s *Service) Sync(ctx context.Context) error {
	if !s.syncMu.TryLock() {
		return nil
	}
	defer s.syncMu.Unlock()
	s.mu.Lock()
	enabled := s.config.Enabled
	r := s.remote
	vaults := []Vault{}
	for _, v := range s.vaults {
		vaults = append(vaults, v)
	}
	s.mu.Unlock()
	if !enabled {
		return ErrDisabled
	}
	if r == nil {
		return ErrUnavailable
	}
	var first error
	for _, v := range vaults {
		err := s.syncVault(ctx, r, v)
		s.mu.Lock()
		if err != nil {
			v.Error = err.Error()
			if first == nil {
				first = err
			}
		} else {
			v.Error = ""
			v.SyncedAt = nowUTC()
		}
		if saveErr := s.put("vault", v.ID, v); saveErr == nil {
			s.vaults[v.ID] = v
		}
		s.notify()
		s.mu.Unlock()
	}
	return first
}

// syncVault first pulls remote state, including deletions, before considering local uploads.
func (s *Service) syncVault(ctx context.Context, r Remote, v Vault) error {
	snap, err := r.Snapshot(ctx, v.AccountID)
	if err != nil {
		return err
	}
	remote := map[string]Node{}
	for _, n := range snap.Nodes {
		remote[n.ID] = n
	}
	if _, ok := remote[v.RootID]; !ok {
		return ErrNotFound
	}
	pending := map[string]Node{}
	for _, n := range snap.Nodes {
		if descendant(remote, n.ID, v.RootID) {
			pending[n.ID] = n
		}
	}
	// Parent-before-child discovery gives every remote node a stable local mapping.
	for len(pending) > 0 {
		progressed := false
		for id, n := range pending {
			s.mu.Lock()
			if !s.config.Enabled {
				s.mu.Unlock()
				return ErrDisabled
			}
			parent, ok := s.localParent(v, n.ParentID)
			s.mu.Unlock()
			if !ok {
				continue
			}
			if err = s.pull(ctx, r, v, n, parent, remote); err != nil {
				return err
			}
			delete(pending, id)
			progressed = true
		}
		if !progressed {
			return ErrInvalid
		}
	}
	s.mu.Lock()
	ids := []string{}
	for id, e := range s.entries {
		if e.VaultID != v.ID {
			continue
		}
		if e.RemoteID != "" {
			if _, ok := remote[e.RemoteID]; !ok {
				e.Remote = nil
				if e.Status == "synced" {
					e.Deleted = true
					e.Status = "remote_deleted"
				} else if e.Status != "remote_deleted" {
					e.Status = "conflict"
				}
				stamp(&e)
				if err = s.saveEntry(e); err != nil {
					s.mu.Unlock()
					return err
				}
			}
		}
		if s.effectivelyDeleted(e) && !e.Deleted {
			continue
		}
		if e.Status == "pending" || e.Uncertain {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.entries[ids[i]], s.entries[ids[j]]
		if a.Directory != b.Directory {
			return a.Directory
		}
		if a.Attachment != b.Attachment {
			return a.Attachment
		}
		return a.Modified < b.Modified
	})
	s.mu.Unlock()
	for index, id := range ids {
		// Bounded passes checkpoint progress instead of timing out one huge migration transaction.
		if index >= 12 {
			break
		}
		if err = s.push(ctx, r, v, id); err != nil {
			return err
		}
	}
	return nil
}

// descendant recognizes only nodes inside this vault and rejects remote cycles.
func descendant(nodes map[string]Node, id, root string) bool {
	for depth := 0; id != "" && depth < 128; depth++ {
		n, ok := nodes[id]
		if !ok {
			return false
		}
		if n.ParentID == root {
			return true
		}
		id = n.ParentID
	}
	return false
}

// localParent maps remote roots and parents while the caller holds the local lock.
func (s *Service) localParent(v Vault, remoteID string) (string, bool) {
	if remoteID == v.RootID {
		return "", true
	}
	for _, e := range s.entries {
		if e.VaultID == v.ID && e.RemoteID == remoteID {
			return e.ID, true
		}
	}
	return "", false
}

// sameNode compares real metadata and immutable content version, never wall clocks.
func sameNode(a, b *Node) bool {
	return a != nil && b != nil && a.ID == b.ID && a.ParentID == b.ParentID && a.Name == b.Name && a.BlobID == b.BlobID && a.Directory == b.Directory
}

// pull fetches changed text outside the lock and checks localRevision again before installing it.
func (s *Service) pull(ctx context.Context, r Remote, v Vault, n Node, parent string, nodes map[string]Node) error {
	deleted := false
	for id, depth := n.ID, 0; id != "" && depth < 128; depth++ {
		node, ok := nodes[id]
		if !ok {
			break
		}
		if node.Name == ".czl-trash" {
			deleted = true
			break
		}
		id = node.ParentID
	}
	s.mu.Lock()
	var e Entry
	found := false
	for _, candidate := range s.entries {
		if candidate.VaultID == v.ID && candidate.RemoteID == n.ID {
			e = clone(candidate)
			found = true
			break
		}
	}
	if found && (e.Deleted == deleted || e.Status == "pending") && (sameNode(e.Base, &n) || e.Status == "conflict" && sameNode(e.Remote, &n)) {
		s.mu.Unlock()
		return nil
	}
	revision := e.Revision
	s.mu.Unlock()
	isText := !n.Directory && strings.EqualFold(extension(n.Name), ".md")
	content := ""
	if isText {
		data, err := r.Download(ctx, v.AccountID, n.BlobID, MaxText)
		if err != nil {
			return err
		}
		content = string(data)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return ErrDisabled
	}
	if found {
		current := s.entries[e.ID]
		if current.Revision != revision {
			return nil
		}
		e = current
	} else {
		// Recover an acknowledged-but-lost create only when its persisted exact intent matches.
		e = Entry{ID: newID(), VaultID: v.ID, RemoteID: n.ID, Status: "synced"}
		for _, local := range s.entries {
			if local.VaultID == v.ID && local.RemoteID == "" && local.ParentID == parent && strings.EqualFold(local.Name, n.Name) {
				intent := local.Intent
				if !local.Uncertain || intent == nil || intent.ParentID != n.ParentID || intent.Name != n.Name || intent.BlobID != n.BlobID || intent.Directory != n.Directory {
					return ErrUncertain
				}
				e = clone(local)
				e.RemoteID = n.ID
				e.Uncertain = false
				e.Intent = nil
				found = true
				if e.Name == n.Name && e.ParentID == parent && (isText && e.Content == content || !isText) {
					e.Status = "synced"
				}
				break
			}
		}
	}
	// The server may normalize an uploaded blob ID after it is attached to a FileNode.
	// Preserve a newer local draft when the downloaded body is exactly the acknowledged base.
	if found && e.Status == "pending" && isText && e.Base != nil && e.BaseHash != "" && digest(content) == e.BaseHash && e.Base.Name == n.Name && e.Base.ParentID == n.ParentID && e.Deleted == deleted {
		e.Base = &n
		e.Remote = &n
		return s.saveEntry(e)
	}
	if found && e.Status != "synced" && !(isText && e.Content == content && e.ParentID == parent && e.Name == n.Name && e.Deleted == deleted) {
		e.Remote = &n
		e.RemoteContent = content
		e.Status = "conflict"
		stamp(&e)
		return s.saveEntry(e)
	}
	if deleted && !e.Deleted {
		e.OriginalParent = e.ParentID
	}
	e.ParentID = parent
	if !deleted || e.Name == "" {
		e.Name = n.Name
	}
	e.Directory = n.Directory
	e.Attachment = !n.Directory && !isText
	e.Type = n.Type
	e.Size = n.Size
	if e.Attachment && e.Base != nil && e.Base.BlobID != n.BlobID {
		e.BlobKey = ""
	}
	e.Content = content
	if isText {
		e.BaseHash = digest(content)
	}
	e.Base = &n
	e.Remote = &n
	e.RemoteContent = ""
	e.Status = "synced"
	e.Deleted = deleted
	stamp(&e)
	return s.saveEntry(e)
}

// push rechecks the current remote object and uses the exact snapshot state as a precondition.
func (s *Service) push(ctx context.Context, r Remote, v Vault, id string) error {
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return ErrDisabled
	}
	e, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if e.Status != "pending" && !e.Uncertain {
		s.mu.Unlock()
		return nil
	}
	parent := v.RootID
	if e.ParentID != "" {
		p := s.entries[e.ParentID]
		if p.RemoteID == "" {
			s.mu.Unlock()
			return nil
		}
		parent = p.RemoteID
	}
	if !e.Directory && !e.Attachment {
		for _, a := range s.entries {
			if a.VaultID == v.ID && a.Attachment && a.Status != "synced" {
				s.mu.Unlock()
				return nil
			}
		}
	}
	s.mu.Unlock()
	var snap Snapshot
	var err error
	if inspector, ok := r.(interface {
		Inspect(context.Context, string, string) (Snapshot, error)
	}); ok && !e.Deleted {
		snap, err = inspector.Inspect(ctx, v.AccountID, e.RemoteID)
	} else {
		snap, err = r.Snapshot(ctx, v.AccountID)
	}
	if err != nil {
		return err
	}
	if e.Deleted {
		trash := ""
		for _, n := range snap.Nodes {
			if n.ParentID == v.RootID && n.Name == ".czl-trash" && n.Directory {
				trash = n.ID
				break
			}
		}
		if trash == "" {
			_, err = r.Set(ctx, v.AccountID, snap.State, "", Node{Name: ".czl-trash", ParentID: v.RootID, Directory: true})
			if err != nil {
				return err
			}
			return nil
		}
		parent = trash
	}
	var current *Node
	for _, n := range snap.Nodes {
		if n.ID == e.RemoteID {
			copy := n
			current = &copy
		}
	}
	if e.RemoteID != "" && !sameNode(e.Base, current) {
		return ErrConflict
	}
	if e.Uncertain && e.RemoteID == "" {
		return ErrUncertain
	}
	blob := ""
	if !e.Directory {
		data := []byte(e.Content)
		if e.Attachment {
			data, err = s.attachmentBytes(e)
			if err != nil {
				return err
			}
		}
		blob, err = r.Upload(ctx, v.AccountID, data)
		if err != nil {
			return err
		}
	}
	n := Node{ID: e.RemoteID, ParentID: parent, Name: e.Name, Directory: e.Directory, BlobID: blob, Type: e.Type}
	if e.Deleted {
		n.Name = e.ID + "--" + e.Name
	}
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return ErrDisabled
	}
	latest := s.entries[id]
	if latest.Revision != e.Revision {
		s.mu.Unlock()
		return nil
	}
	if e.RemoteID == "" {
		latest.Uncertain = true
		latest.Intent = &n
		if err = s.saveEntry(latest); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	result, err := r.Set(ctx, v.AccountID, snap.State, e.RemoteID, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	latest = s.entries[id]
	if err != nil {
		if !errors.Is(err, ErrUncertain) {
			latest.Uncertain = false
			latest.Intent = nil
			if saveErr := s.saveEntry(latest); saveErr != nil {
				return saveErr
			}
		}
		return err
	}
	latest.RemoteID = result.ID
	latest.Base = &result
	if !e.Directory && !e.Attachment {
		latest.BaseHash = digest(e.Content)
	}
	latest.Intent = nil
	latest.Remote = &result
	latest.Uncertain = false
	if latest.Revision == e.Revision {
		latest.Status = "synced"
	}
	return s.saveEntry(latest)
}

// extension is deliberately case-insensitive at call sites for cross-platform Markdown names.
func extension(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index < 0 {
		return ""
	}
	return name[index:]
}
