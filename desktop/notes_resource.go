package main

import (
	"net/http"
	"strconv"

	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
)

// serveNoteResource resolves only vault-relative resources and blocks active content.
func (a *App) serveNoteResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s, err := a.noteService()
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	data, _, err := s.Attachment(r.Context(), q.Get("vault"), q.Get("note"), q.Get("path"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	kind := notes.PreviewType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "application/pdf":
	default:
		http.Error(w, "unsupported", http.StatusUnsupportedMediaType)
		return
	}
	if len(data) > notes.MaxAttachment {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Write(data)
}
