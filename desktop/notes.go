package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

const EventNotesChanged = "notes:changed"
const EventNotesConfigChanged = "notes:config-changed"

// noteState isolates libraries by server and login; profile switches cannot expose another user's notes.
type noteState struct {
	sync.Mutex
	services map[string]*notes.Service
	wake     chan struct{}
}

// noteService lazily opens the durable profile store independently of network availability.
func (a *App) noteService() (*notes.Service, error) {
	a.mu.RLock()
	cfg := a.cfg
	st := a.store
	a.mu.RUnlock()
	if st == nil || !cfg.Configured() {
		return nil, notes.ErrUnavailable
	}
	sum := sha256.Sum256([]byte(cfg.SessionEndpoint + "\x00" + cfg.Username))
	key := hex.EncodeToString(sum[:])
	a.notes.Lock()
	defer a.notes.Unlock()
	if a.notes.services == nil {
		a.notes.services = map[string]*notes.Service{}
	}
	if service := a.notes.services[key]; service != nil {
		return service, nil
	}
	root, err := config.Dir()
	if err != nil {
		return nil, notes.ErrStorage
	}
	service, err := notes.Open(filepath.Join(root, "notes", key), func() { a.emit(EventNotesChanged) })
	if err != nil {
		return nil, err
	}
	a.notes.services[key] = service
	return service, nil
}

// wakeNotes coalesces edits and FileNode notifications instead of launching overlapping workers.
func (a *App) wakeNotes() {
	a.notes.Lock()
	wake := a.notes.wake
	a.notes.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// runNotes catches push changes and performs periodic reconciliation even while the pane is hidden.
func (a *App) runNotes(ctx context.Context, s *syncer.Syncer) {
	service, err := a.noteService()
	if err != nil {
		return
	}
	service.SetRemote(noteRemote{s: s})
	wake := make(chan struct{}, 1)
	a.notes.Lock()
	a.notes.wake = wake
	a.notes.Unlock()
	wake <- struct{}{}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer func() {
		a.notes.Lock()
		if a.notes.wake == wake {
			a.notes.wake = nil
		}
		a.notes.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
		// Let one burst of keystrokes/notifications settle. A quiet sync must not schedule itself.
		select {
		case <-ctx.Done():
			return
		case <-time.After(750 * time.Millisecond):
		}
		if !service.GetConfig().Enabled {
			continue
		}
		work, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = service.Sync(work)
		cancel()
		if err != nil && ctx.Err() == nil {
			a.log.Warn("notes sync incomplete", "err", err)
		}
		if err == nil && service.HasPending() {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}

// noteRemote adapts the existing authenticated JMAP connection without exposing credentials.
type noteRemote struct{ s *syncer.Syncer }

// Snapshot loads remote metadata with a consistent state token.
func (r noteRemote) Snapshot(ctx context.Context, account string) (notes.Snapshot, error) {
	return r.s.FileSnapshot(ctx, account)
}

// Download bounds bytes before returning untrusted content.
func (r noteRemote) Download(ctx context.Context, account, blob string, limit int64) ([]byte, error) {
	rc, err := r.s.DownloadBlob(ctx, account, blob)
	if err != nil {
		return nil, notes.ErrUnavailable
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, notes.ErrUnavailable
	}
	if int64(len(data)) > limit {
		return nil, notes.ErrInvalid
	}
	return data, nil
}

// Upload checks the advertised account-independent server upload ceiling.
func (r noteRemote) Upload(ctx context.Context, account string, data []byte) (string, error) {
	limit := r.s.MaxUploadBytes()
	if limit > 0 && int64(len(data)) > limit {
		return "", notes.ErrInvalid
	}
	blob, _, _, err := r.s.UploadBlob(ctx, account, bytes.NewReader(data))
	if err != nil {
		return "", notes.ErrUnavailable
	}
	return blob, nil
}

// Inspect obtains a target object and state without enumerating the whole account for each upload.
func (r noteRemote) Inspect(ctx context.Context, account, id string) (notes.Snapshot, error) {
	return r.s.InspectFile(ctx, account, id)
}

// Set requires the observed FileNode state for both creates and updates.
func (r noteRemote) Set(ctx context.Context, account, state, id string, n notes.Node) (notes.Node, error) {
	return r.s.SetFileConditional(ctx, account, state, id, n)
}
