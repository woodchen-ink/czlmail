package notes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Export snapshots local originals; missing remote attachments fail rather than claiming a complete backup.
func (s *Service) Export(ctx context.Context, vault, target string) error {
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return ErrDisabled
	}
	v, ok := s.vaults[vault]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	r := s.remote
	entries := map[string]Entry{}
	for id, e := range s.entries {
		if e.VaultID == vault && !s.effectivelyDeleted(e) {
			entries[id] = clone(e)
		}
	}
	s.mu.Unlock()
	if err := os.Mkdir(target, 0700); err != nil {
		return ErrStorage
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		parts := []string{e.Name}
		parent := e.ParentID
		for depth := 0; parent != ""; depth++ {
			if depth > 128 {
				return ErrInvalid
			}
			p, ok := entries[parent]
			if !ok {
				return ErrInvalid
			}
			parts = append([]string{p.Name}, parts...)
			parent = p.ParentID
		}
		for _, part := range parts {
			if !validName(part) {
				return ErrInvalid
			}
		}
		destination := filepath.Join(append([]string{target}, parts...)...)
		rel, err := filepath.Rel(target, destination)
		if err != nil || strings.HasPrefix(rel, "..") {
			return ErrInvalid
		}
		if e.Directory {
			if err = os.MkdirAll(destination, 0700); err != nil {
				return ErrStorage
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return ErrStorage
		}
		data := []byte(e.Content)
		if e.Attachment {
			if e.BlobKey != "" {
				data, err = s.attachmentBytes(e)
			} else if r != nil && e.Base != nil {
				data, err = r.Download(ctx, v.AccountID, e.Base.BlobID, MaxAttachment)
			} else {
				return ErrUnavailable
			}
			if err != nil {
				return err
			}
		}
		f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return ErrStorage
		}
		_, err = f.Write(data)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return ErrStorage
		}
	}
	return nil
}
