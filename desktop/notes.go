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
	// wake carries edits, urgent everything else; see wakeNotes.
	wake   chan struct{}
	urgent chan struct{}
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

// wakeNotes asks for a sync soon. Edits are urgent=false: they wait for typing to pause, so a
// note being written is not uploaded every second. Everything else (remote changes, leaving a
// note, manual actions) is urgent. Requests coalesce; overlapping workers are never launched.
func (a *App) wakeNotes(urgent bool) {
	a.notes.Lock()
	wake, now := a.notes.wake, a.notes.urgent
	a.notes.Unlock()
	if urgent {
		wake = now
	}
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

const (
	noteEditQuiet  = 3 * time.Second        // upload after typing has paused this long
	noteEditLimit  = 15 * time.Second       // but never hold an edit back longer than this
	noteSettle     = 750 * time.Millisecond // lets one burst of notifications coalesce
	noteReconcile  = time.Minute            // periodic check for missed push notifications
	noteRoundLimit = 10 * time.Minute       // one pass; each upload has its own shorter limit
)

// runNotes catches push changes and performs periodic reconciliation even while the pane is hidden.
func (a *App) runNotes(ctx context.Context, s *syncer.Syncer) {
	service, err := a.noteService()
	if err != nil {
		return
	}
	service.SetRemote(noteRemote{s: s})
	// A newer build may be able to send what an older one could not: retry recorded failures once.
	if err = service.ResetFailuresOnce(version); err != nil {
		a.log.Warn("notes failure reset", "err", err)
	}
	wake, urgent := make(chan struct{}, 1), make(chan struct{}, 1)
	a.notes.Lock()
	a.notes.wake, a.notes.urgent = wake, urgent
	a.notes.Unlock()
	defer func() {
		a.notes.Lock()
		if a.notes.wake == wake {
			a.notes.wake, a.notes.urgent = nil, nil
		}
		a.notes.Unlock()
	}()
	next := time.NewTimer(0)
	defer next.Stop()
	for {
		delay := noteSettle
		select {
		case <-ctx.Done():
			return
		case <-wake:
			delay = noteEditQuiet
		case <-urgent:
		case <-next.C:
			delay = 0
		}
		// Wait for the burst to end: more edits push the start back (up to a limit), anything
		// urgent pulls it forward.
		start := time.Now()
		due := start.Add(delay)
		for wait := time.Until(due); wait > 0; wait = time.Until(due) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			case <-wake:
				if later := time.Now().Add(noteEditQuiet); later.After(due) {
					due = later
				}
				if limit := start.Add(noteEditLimit); due.After(limit) {
					due = limit
				}
			case <-urgent:
				if sooner := time.Now().Add(noteSettle); sooner.Before(due) {
					due = sooner
				}
			}
		}
		pause := noteReconcile
		if service.GetConfig().Enabled {
			work, cancel := context.WithTimeout(ctx, noteRoundLimit)
			round, err := service.SyncRound(work, false)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.log.Warn("notes sync incomplete", "err", err)
			}
			// Continue at once only after real progress; a pass that moved nothing waits for the
			// next change, a due retry or the periodic check, so a stuck entry cannot spin the loop.
			if err == nil && round.More {
				pause = 0
			} else if round.RetryAt > 0 {
				if until := time.Until(time.UnixMilli(round.RetryAt)); until < pause {
					pause = max(until, time.Second)
				}
			}
		}
		if !next.Stop() {
			select {
			case <-next.C:
			default:
			}
		}
		next.Reset(pause)
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
		return nil, notes.ErrTooLarge
	}
	return data, nil
}

// Upload checks the advertised account-independent server upload ceiling.
func (r noteRemote) Upload(ctx context.Context, account string, data []byte) (string, error) {
	limit := r.s.MaxUploadBytes()
	if limit > 0 && int64(len(data)) > limit {
		return "", notes.ErrTooLarge
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

// Changes lists what moved since a known state, so the periodic check need not list every file.
func (r noteRemote) Changes(ctx context.Context, account, since string) (notes.Delta, error) {
	return r.s.FileChanges(ctx, account, since)
}

// Set requires the observed FileNode state for both creates and updates.
func (r noteRemote) Set(ctx context.Context, account, state, id string, n notes.Node) (notes.Node, error) {
	return r.s.SetFileConditional(ctx, account, state, id, n)
}
