package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
)

// OpenNoteLink navigates Markdown or safely opens an attachment; executable files are only revealed.
func (a *App) OpenNoteLink(vault, id, relative string) (notes.Entry, error) {
	s, err := a.noteService()
	if err != nil {
		return notes.Entry{}, err
	}
	e, err := s.ResolveLink(vault, id, relative)
	if err != nil {
		return notes.Entry{}, err
	}
	if !e.Attachment {
		return e, nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	data, _, err := s.Attachment(ctx, vault, id, relative)
	if err != nil {
		return notes.Entry{}, err
	}
	root, err := config.Dir()
	if err != nil {
		return notes.Entry{}, notes.ErrStorage
	}
	root = filepath.Join(root, "note-open", e.ID)
	if err = os.MkdirAll(root, 0700); err != nil {
		return notes.Entry{}, notes.ErrStorage
	}
	target := filepath.Join(root, e.Name)
	if err = os.WriteFile(target, data, 0600); err != nil {
		return notes.Entry{}, notes.ErrStorage
	}
	if dangerousExt[strings.ToLower(filepath.Ext(e.Name))] {
		err = platform.RevealPath(target)
	} else {
		err = platform.OpenPath(target)
	}
	if err != nil {
		return notes.Entry{}, notes.ErrUnavailable
	}
	return e, nil
}

// ImportNoteFolder uses a native picker for explicit filesystem consent; MCP only accepts provided bytes.
func (a *App) ImportNoteFolder(vault string) (notes.ImportResult, error) {
	root, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "导入 Markdown 笔记库"})
	if err != nil || root == "" {
		return notes.ImportResult{}, err
	}
	result := notes.ImportResult{SavedLocally: true}
	count := 0
	err = filepath.WalkDir(root, func(file string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return notes.ErrStorage
		}
		if file == root {
			return nil
		}
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			if item.IsDir() {
				return filepath.SkipDir
			}
			result.Skipped++
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return notes.ErrInvalid
		}
		if item.IsDir() {
			return nil
		}
		count++
		if count > 5000 {
			return notes.ErrInvalid
		}
		info, err := item.Info()
		if err != nil || info.Size() > notes.MaxAttachment {
			return notes.ErrInvalid
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return notes.ErrInvalid
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return notes.ErrStorage
		}
		one, err := a.ImportNoteFiles(vault, []notes.ImportedFile{{Path: filepath.ToSlash(relative), Data: data}})
		result.Imported += one.Imported
		result.Skipped += one.Skipped
		return err
	})
	return result, err
}

// ExportNoteVault writes to a new subdirectory, never overwriting the selected folder's files.
func (a *App) ExportNoteVault(vault string) (string, error) {
	root, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "导出笔记库"})
	if err != nil || root == "" {
		return "", err
	}
	s, err := a.noteService()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	target := filepath.Join(root, "CZL-Notes-"+time.Now().Format("20060102-150405"))
	if err = s.Export(ctx, vault, target); err != nil {
		return target, err
	}
	return target, nil
}
