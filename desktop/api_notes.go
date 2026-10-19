package main

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// GetNoteConfig reads device-local gates even when the note pane has never been visited.
func (a *App) GetNoteConfig() (notes.Config, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Config{}, err
	}
	return s.GetConfig(), nil
}

// SetNoteConfig applies backend gates before refreshing MCP sessions and navigation.
func (a *App) SetNoteConfig(c notes.Config) error {
	s, err := a.noteService()
	if err != nil {
		return err
	}
	if err = s.Configure(c); err != nil {
		return err
	}
	a.emit(EventNotesConfigChanged, c)
	a.wakeNotes()
	a.stopMCP()
	if a.settingOn(store.SettingMCPEnabled, false) {
		return a.startMCP()
	}
	return nil
}

// ListNoteVaults lists configured roots without modifying the server.
func (a *App) ListNoteVaults() ([]notes.Vault, error) {
	s, err := a.noteService()
	if err != nil {
		return nil, err
	}
	return s.Vaults()
}

// BindNoteVault explicitly binds a writable personal-account root.
func (a *App) BindNoteVault(account, root string) (notes.Vault, error) {
	accounts, err := a.ListAccounts()
	if err != nil {
		return notes.Vault{}, err
	}
	allowed := false
	for _, acc := range accounts {
		if acc.ID == account && acc.IsPersonal {
			allowed = true
		}
	}
	if !allowed {
		return notes.Vault{}, notes.ErrForbidden
	}
	s, err := a.noteService()
	if err != nil {
		return notes.Vault{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	v, err := s.Bind(ctx, account, root)
	a.wakeNotes()
	return v, err
}

// ListNotes returns current local metadata and supports literal full-text search.
func (a *App) ListNotes(vault, parent, query string, trash bool) ([]notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return nil, err
	}
	return s.List(vault, parent, query, trash)
}

// ReadNote returns the latest durable local revision.
func (a *App) ReadNote(vault, id string) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	return s.Read(vault, id)
}

// CreateNote accepts a local Markdown document or directory without waiting for upload.
func (a *App) CreateNote(in notes.Edit, directory bool) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	e, err := s.Create(in, directory)
	if err == nil {
		a.wakeNotes()
	}
	return e, err
}

// UpdateNote rejects stale editor revisions.
func (a *App) UpdateNote(in notes.Edit) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	e, err := s.Update(in)
	if err == nil {
		a.wakeNotes()
	}
	return e, err
}

// MoveNote shares validation and reference-safety rules with MCP.
func (a *App) MoveNote(in notes.Edit) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	e, err := s.Move(in)
	if err == nil {
		a.wakeNotes()
	}
	return e, err
}

// TrashNote soft-deletes or restores a document; it never invokes FileNode/destroy.
func (a *App) TrashNote(vault, id string, revision int64, restore bool) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	e, err := s.Trash(vault, id, revision, restore)
	if err == nil {
		a.wakeNotes()
	}
	return e, err
}

// CompareNote distinguishes current-server differences from cached offline differences.
func (a *App) CompareNote(vault, id string) (notes.Diff, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Diff{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return s.Compare(ctx, vault, id)
}

// ResolveNote preserves a local recovery copy before resolving a version conflict.
func (a *App) ResolveNote(vault, id string, revision int64, remoteVersion, strategy, content string) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	e, err := s.Resolve(ctx, vault, id, revision, remoteVersion, strategy, content)
	if err == nil {
		a.wakeNotes()
	}
	return e, err
}

// SyncNotes is a manual retry in addition to the push/periodic worker.
func (a *App) SyncNotes() error {
	s, err := a.noteService()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return s.Sync(ctx)
}

// AddNoteAttachment persists clipboard bytes before returning a Markdown fragment.
func (a *App) AddNoteAttachment(vault, id string, revision int64, name, encoded string) (notes.AttachmentResult, error) {
	if len(encoded) > ((notes.MaxAttachment+2)/3)*4 {
		return notes.AttachmentResult{}, notes.ErrInvalid
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return notes.AttachmentResult{}, notes.ErrInvalid
	}
	s, err := a.noteService()
	if err != nil {
		return notes.AttachmentResult{}, err
	}
	out, err := s.AddAttachment(vault, id, revision, name, data)
	if err == nil {
		a.wakeNotes()
	}
	return out, err
}

// AttachNotePaths is a UI-only file-drop bridge; no MCP tool exposes arbitrary local paths.
func (a *App) AttachNotePaths(vault, id string, revision int64, paths []string) ([]notes.AttachmentResult, error) {
	if len(paths) > 100 {
		return nil, notes.ErrInvalid
	}
	s, err := a.noteService()
	if err != nil {
		return nil, err
	}
	out := []notes.AttachmentResult{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return out, notes.ErrInvalid
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > notes.MaxAttachment {
			f.Close()
			return out, notes.ErrInvalid
		}
		data, err := io.ReadAll(io.LimitReader(f, notes.MaxAttachment+1))
		f.Close()
		if err != nil {
			return out, notes.ErrInvalid
		}
		item, err := s.AddAttachment(vault, id, revision, info.Name(), data)
		if err != nil {
			return out, err
		}
		out = append(out, item)
	}
	a.wakeNotes()
	return out, nil
}

// ImportNoteFiles copies bytes from UI or authorized MCP clients, retaining the source externally.
func (a *App) ImportNoteFiles(vault string, files []notes.ImportedFile) (notes.ImportResult, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.ImportResult{}, err
	}
	result, err := s.Import(vault, files)
	a.wakeNotes()
	return result, err
}
