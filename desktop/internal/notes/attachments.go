package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ImportedFile contains original bytes and a vault-relative destination, never a local source path.
type ImportedFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

// ImportResult records copied files and retries without claiming any remote commit.
type ImportResult struct {
	Imported     int  `json:"imported"`
	Skipped      int  `json:"skipped"`
	SavedLocally bool `json:"savedLocally"`
}

// AttachmentResult provides the exact Markdown fragment after local attachment persistence.
type AttachmentResult struct {
	Entry        Entry  `json:"entry"`
	RelativePath string `json:"relativePath"`
	Markdown     string `json:"markdown"`
}

// storeBlob writes a content-addressed attachment atomically before any reference is accepted.
func (s *Service) storeBlob(data []byte) (string, error) {
	if len(data) > MaxAttachment {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.root, "blobs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", ErrStorage
	}
	target := filepath.Join(dir, key)
	if _, err := os.Stat(target); err == nil {
		return key, nil
	}
	f, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return "", ErrStorage
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", ErrStorage
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return "", ErrStorage
	}
	return key, nil
}

// importDirectory ensures each path component is a live directory in this vault.
func (s *Service) importDirectory(vault, relative string) (string, error) {
	parent := ""
	if relative == "." || relative == "" {
		return parent, nil
	}
	for _, part := range strings.Split(relative, "/") {
		if !validName(part) || part == ".czl-trash" {
			return "", ErrInvalid
		}
		found := ""
		for _, e := range s.entries {
			if e.VaultID == vault && e.ParentID == parent && strings.EqualFold(e.Name, part) && !e.Deleted {
				if !e.Directory {
					return "", ErrInvalid
				}
				found = e.ID
				break
			}
		}
		if found == "" {
			e := Entry{ID: newID(), VaultID: vault, ParentID: parent, Name: part, Directory: true, Status: "pending"}
			stamp(&e)
			if err := s.commit(e, "", ""); err != nil {
				return "", err
			}
			found = e.ID
		}
		parent = found
	}
	return parent, nil
}

// Import copies original bytes into the private library; an identical retry does not duplicate files.
func (s *Service) Import(vault string, files []ImportedFile) (ImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ImportResult{SavedLocally: true}
	if !s.config.Enabled {
		return result, ErrDisabled
	}
	if _, ok := s.vaults[vault]; !ok {
		return result, ErrNotFound
	}
	if len(files) > 100 {
		return result, ErrInvalid
	}
	for _, file := range files {
		relative := file.Path
		if strings.Contains(relative, "\\") || strings.HasPrefix(relative, "/") || path.Clean(relative) != relative {
			return result, ErrInvalid
		}
		for _, part := range strings.Split(relative, "/") {
			if !validName(part) || strings.HasPrefix(part, ".obsidian") || part == ".git" || part == ".czl-trash" {
				return result, ErrInvalid
			}
		}
		parent, err := s.importDirectory(vault, path.Dir(relative))
		if err != nil {
			return result, err
		}
		name := path.Base(relative)
		isMD := strings.EqualFold(extension(name), ".md")
		if len(file.Data) > MaxAttachment || isMD && (len(file.Data) > MaxText || !utf8.Valid(file.Data)) {
			return result, ErrInvalid
		}
		e := Entry{ID: newID(), VaultID: vault, ParentID: parent, Name: name, Attachment: !isMD, Status: "pending", Size: int64(len(file.Data)), Type: http.DetectContentType(file.Data)}
		if isMD {
			e.Content = string(file.Data)
			e.Type = "text/markdown"
		} else {
			e.BlobKey, err = s.storeBlob(file.Data)
			if err != nil {
				return result, err
			}
		}
		duplicate := false
		for _, old := range s.entries {
			if old.VaultID == vault && old.ParentID == parent && strings.EqualFold(old.Name, name) && !old.Deleted {
				if old.Content == e.Content && old.BlobKey == e.BlobKey {
					duplicate = true
					break
				}
				return result, ErrConflict
			}
		}
		if duplicate {
			result.Skipped++
			continue
		}
		stamp(&e)
		if err = s.commit(e, "", ""); err != nil {
			return result, err
		}
		result.Imported++
	}
	return result, nil
}

// AddAttachment creates a unique asset without silently inserting into an active editor buffer.
func (s *Service) AddAttachment(vault, id string, revision int64, name string, data []byte) (AttachmentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	note, err := s.entry(vault, id)
	if err != nil {
		return AttachmentResult{}, err
	}
	if note.Revision != revision {
		return AttachmentResult{}, ErrRevision
	}
	if s.effectivelyDeleted(note) || note.Directory || note.Attachment || !validName(name) {
		return AttachmentResult{}, ErrInvalid
	}
	assets := ""
	for _, e := range s.entries {
		if e.VaultID == vault && e.ParentID == note.ParentID && e.Name == "assets" && !e.Deleted {
			if !e.Directory {
				return AttachmentResult{}, ErrInvalid
			}
			assets = e.ID
		}
	}
	if assets == "" {
		folder := Entry{ID: newID(), VaultID: vault, ParentID: note.ParentID, Name: "assets", Directory: true, Status: "pending"}
		stamp(&folder)
		if err = s.commit(folder, "", ""); err != nil {
			return AttachmentResult{}, err
		}
		assets = folder.ID
	}
	key, err := s.storeBlob(data)
	if err != nil {
		return AttachmentResult{}, err
	}
	ext := extension(name)
	name = strings.TrimSuffix(name, ext) + "-" + newID()[:8] + ext
	e := Entry{ID: newID(), VaultID: vault, ParentID: assets, Name: name, Attachment: true, BlobKey: key, Type: http.DetectContentType(data), Size: int64(len(data)), Status: "pending"}
	stamp(&e)
	if err = s.commit(e, "", ""); err != nil {
		return AttachmentResult{}, err
	}
	relative := "assets/" + name
	encoded := "assets/" + url.PathEscape(name)
	fragment := "[" + strings.NewReplacer("[", "\\[", "]", "\\]").Replace(name) + "](" + encoded + ")"
	if safeImage(e.Type) {
		fragment = "!" + fragment
	}
	return AttachmentResult{Entry: e, RelativePath: relative, Markdown: fragment}, nil
}

// safeImage permits only non-active image formats for inline display.
func safeImage(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return true
	}
	return false
}

// Attachment resolves a Markdown-relative resource inside this library only.
func (s *Service) Attachment(ctx context.Context, vault, noteID, relative string) ([]byte, string, error) {
	s.mu.Lock()
	target, err := s.resolveLink(vault, noteID, relative)
	v := s.vaults[vault]
	r := s.remote
	s.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	if target.ID == "" || target.Directory {
		return nil, "", ErrNotFound
	}
	if target.BlobKey != "" {
		b, err := s.attachmentBytes(target)
		return b, target.Type, err
	}
	if target.Base == nil || r == nil {
		return nil, "", ErrUnavailable
	}
	b, err := r.Download(ctx, v.AccountID, target.Base.BlobID, MaxAttachment)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.entries[target.ID]
	if current.Revision != target.Revision {
		return nil, "", ErrRevision
	}
	key, err := s.storeBlob(b)
	if err != nil {
		return nil, "", err
	}
	current.BlobKey = key
	if err = s.saveEntry(current); err != nil {
		return nil, "", err
	}
	return b, target.Type, nil
}
