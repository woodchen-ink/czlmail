package notes

import (
	"context"
	"os"
	"path/filepath"
)

// Compare reads live remote metadata where possible and clearly labels an offline cached result.
func (s *Service) Compare(ctx context.Context, vault, id string) (Diff, error) {
	s.mu.Lock()
	e, err := s.entry(vault, id)
	v := s.vaults[vault]
	r := s.remote
	s.mu.Unlock()
	if err != nil {
		return Diff{}, err
	}
	d := Diff{Local: e, Remote: e.Remote, RemoteContent: e.RemoteContent, Stale: true}
	if e.Status == "synced" {
		d.Remote = e.Base
		d.RemoteContent = e.Content
	}
	if r != nil && e.RemoteID != "" {
		snap, remoteErr := r.Snapshot(ctx, v.AccountID)
		if remoteErr == nil {
			d.Remote = nil
			d.RemoteContent = ""
			d.Stale = false
			for _, n := range snap.Nodes {
				if n.ID == e.RemoteID {
					copy := n
					d.Remote = &copy
					break
				}
			}
			if d.Remote != nil && !e.Directory && !e.Attachment {
				data, downloadErr := r.Download(ctx, v.AccountID, d.Remote.BlobID, MaxText)
				if downloadErr != nil {
					return Diff{}, downloadErr
				}
				d.RemoteContent = string(data)
			}
		}
	}
	d.RemoteVersion = digest(d.Remote)
	return d, nil
}

// Resolve protects both sides again and saves a recovery copy before changing local content.
func (s *Service) Resolve(ctx context.Context, vault, id string, revision int64, remoteVersion, strategy, content string) (Entry, error) {
	d, err := s.Compare(ctx, vault, id)
	if err != nil {
		return Entry{}, err
	}
	if d.Stale {
		return Entry{}, ErrUnavailable
	}
	if d.RemoteVersion != remoteVersion {
		return Entry{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(vault, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Revision != revision {
		return Entry{}, ErrRevision
	}
	if e.Directory || e.Attachment {
		return Entry{}, ErrInvalid
	}
	recovery := clone(e)
	recovery.ID = newID()
	recovery.RemoteID = ""
	recovery.Base = nil
	recovery.BaseHash = ""
	recovery.Intent = nil
	recovery.Remote = nil
	recovery.Uncertain = false
	recovery.Name = "recovery-" + recovery.ID[:8] + "-" + e.Name
	recovery.Status = "pending"
	recovery.Deleted = false
	stamp(&recovery)
	switch strategy {
	case "remote", "local", "both", "merged":
	default:
		return Entry{}, ErrInvalid
	}
	if strategy == "merged" && len(content) > MaxText {
		return Entry{}, ErrInvalid
	}
	// Recovery is committed first: interrupted resolution can leave an extra safe copy, never loss.
	if err = s.commit(recovery, "", ""); err != nil {
		return Entry{}, err
	}
	e.BaseHash = digest(d.RemoteContent)
	e.Intent = nil
	if strategy == "remote" || strategy == "both" {
		if d.Remote == nil {
			e.Deleted = true
			e.Status = "remote_deleted"
		} else {
			e.Content = d.RemoteContent
			e.Name = d.Remote.Name
			e.Status = "synced"
			e.Base = d.Remote
			e.Remote = d.Remote
			e.RemoteContent = ""
		}
	} else {
		if d.Remote == nil {
			e.RemoteID = ""
		}
		e.Base = d.Remote
		e.Remote = d.Remote
		e.Status = "pending"
		if strategy == "merged" {
			e.Content = content
		}
	}
	e.Uncertain = false
	stamp(&e)
	return e, s.commit(e, "", "")
}

// attachmentBytes reads only content-addressed blobs created inside the private library store.
func (s *Service) attachmentBytes(e Entry) ([]byte, error) {
	if len(e.BlobKey) != 64 {
		return nil, ErrStorage
	}
	for _, c := range e.BlobKey {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, ErrStorage
		}
	}
	b, err := os.ReadFile(filepath.Join(s.root, "blobs", e.BlobKey))
	if err != nil {
		return nil, ErrStorage
	}
	return b, nil
}
