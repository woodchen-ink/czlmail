package main

import (
	"context"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
)

// NoteLinkPreview describes safe inline resources without launching any external program.
type NoteLinkPreview struct {
	Entry       notes.Entry `json:"entry"`
	PreviewType string      `json:"previewType"`
}

// PreviewNoteLink validates the library-relative link and sniffs bytes, not extension or remote MIME.
func (a *App) PreviewNoteLink(vault, id, relative string) (NoteLinkPreview, error) {
	service, err := a.noteService()
	if err != nil {
		return NoteLinkPreview{}, err
	}
	entry, err := service.ResolveLink(vault, id, relative)
	if err != nil {
		return NoteLinkPreview{}, err
	}
	result := NoteLinkPreview{Entry: entry}
	if entry.Directory || !entry.Attachment {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	data, _, err := service.Attachment(ctx, vault, id, relative)
	if err != nil {
		return NoteLinkPreview{}, err
	}
	result.PreviewType = notes.PreviewType(data)
	return result, nil
}
