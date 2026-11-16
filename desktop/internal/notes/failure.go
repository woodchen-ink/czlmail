package notes

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// Failure kinds stored in Entry.SyncError. Only "temporary" is retried automatically.
const (
	FailTemporary = "temporary" // network, timeout, 5xx, lost response
	FailRejected  = "rejected"  // the server refused the write
	FailForbidden = "forbidden" // no write permission
	FailQuota     = "quota"
	FailTooLarge  = "too_large"
	FailMissing   = "missing" // the local attachment file is gone
)

// visibleAfter hides brief hiccups: a temporary failure is shown only once it keeps failing.
const visibleAfter = 3

// remoteFailure tracks a remote file that could not be downloaded and has no local entry yet.
type remoteFailure struct {
	vault    string
	node     Node
	kind     string
	detail   string
	attempts int
	retryAt  int64
}

// Problem is one entry that did not synchronize, for the list behind the sync button.
type Problem struct {
	ID         string `json:"id"`
	VaultID    string `json:"vaultId"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	Attempts   int    `json:"attempts"`
	Directory  bool   `json:"directory"`
	Attachment bool   `json:"attachment"`
	// RemoteOnly: a file on the server that could not be downloaded; there is nothing to open locally.
	RemoteOnly bool `json:"remoteOnly"`
}

// classify maps an error to a failure kind and the server's own words for the details view.
func classify(err error) (kind, detail string) {
	detail = err.Error()
	switch {
	case errors.Is(err, ErrRejected), errors.Is(err, ErrInvalid):
		return FailRejected, detail
	case errors.Is(err, ErrForbidden):
		return FailForbidden, detail
	case errors.Is(err, ErrQuota):
		return FailQuota, detail
	case errors.Is(err, ErrTooLarge):
		return FailTooLarge, detail
	case errors.Is(err, ErrStorage):
		return FailMissing, detail
	}
	return FailTemporary, detail
}

// backoff spaces out automatic retries of a temporary failure.
func backoff(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return 5 * time.Second
	case attempts == 2:
		return 30 * time.Second
	case attempts == 3:
		return 2 * time.Minute
	}
	return 10 * time.Minute
}

// fatal reports errors that end the whole round instead of being pinned on one entry.
func fatal(ctx context.Context, err error) bool {
	return errors.Is(err, ErrDisabled) || ctx.Err() != nil
}

// fail records a failed attempt on e; the caller persists it.
func fail(e *Entry, err error) {
	e.SyncError, e.SyncDetail = classify(err)
	e.SyncAttempts++
	e.SyncRetryAt = 0
	if e.SyncError == FailTemporary {
		e.SyncRetryAt = time.Now().Add(backoff(e.SyncAttempts)).UnixMilli()
	}
}

// clearFailure forgets the last failure after a success, an edit or a manual retry.
func clearFailure(e *Entry) {
	e.SyncError, e.SyncDetail, e.SyncAttempts, e.SyncRetryAt = "", "", 0, 0
}

// blocked reports a failure that automatic rounds no longer retry.
func blocked(e Entry) bool { return e.SyncError != "" && e.SyncError != FailTemporary }

// localOnlyTrash: created here, trashed before it was ever uploaded. It stays in the local trash
// and is not sent to the server; restoring it queues a normal create. Caller holds s.mu.
func (s *Service) localOnlyTrash(e Entry) bool {
	return e.RemoteID == "" && !e.Uncertain && s.effectivelyDeleted(e)
}

// queued reports whether an automatic round should try to upload e now or later. Caller holds s.mu.
func (s *Service) queued(e Entry) bool {
	if e.Status != "pending" || blocked(e) || s.localOnlyTrash(e) {
		return false
	}
	return e.Deleted || !s.effectivelyDeleted(e)
}

// problemKind returns what the user should be told about e, or "" when there is nothing to report.
func (s *Service) problemKind(e Entry) string {
	if s.localOnlyTrash(e) {
		return ""
	}
	if e.Status == "conflict" && !s.effectivelyDeleted(e) {
		return "conflict"
	}
	if blocked(e) || e.SyncError == FailTemporary && e.SyncAttempts >= visibleAfter {
		return e.SyncError
	}
	return ""
}

// problemCount is the badge number for one library. Caller holds s.mu.
func (s *Service) problemCount(vault string) int {
	count := 0
	for _, e := range s.entries {
		if e.VaultID == vault && s.problemKind(e) != "" {
			count++
		}
	}
	for _, f := range s.remoteFail {
		if f.vault == vault && (f.kind != FailTemporary || f.attempts >= visibleAfter) {
			count++
		}
	}
	return count
}

// Problems lists everything in a library that did not synchronize, most severe first.
func (s *Service) Problems(vault string) ([]Problem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return nil, ErrDisabled
	}
	if _, ok := s.vaults[vault]; !ok {
		return nil, ErrNotFound
	}
	out := []Problem{}
	for _, e := range s.entries {
		kind := s.problemKind(e)
		if e.VaultID != vault || kind == "" {
			continue
		}
		out = append(out, Problem{ID: e.ID, VaultID: vault, Name: e.Name, Path: s.folderPath(e), Kind: kind, Detail: e.SyncDetail, Attempts: e.SyncAttempts, Directory: e.Directory, Attachment: e.Attachment})
	}
	for _, f := range s.remoteFail {
		if f.vault == vault && (f.kind != FailTemporary || f.attempts >= visibleAfter) {
			p := Problem{VaultID: vault, Name: f.node.Name, Kind: f.kind, Detail: f.detail, Attempts: f.attempts, RemoteOnly: true}
			for _, e := range s.entries {
				if e.VaultID == vault && e.RemoteID == f.node.ID {
					p.ID, p.Name, p.Path, p.RemoteOnly = e.ID, e.Name, s.folderPath(e), false
					break
				}
			}
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == FailTemporary) != (out[j].Kind == FailTemporary) {
			return out[j].Kind == FailTemporary
		}
		return strings.ToLower(out[i].Path+"/"+out[i].Name) < strings.ToLower(out[j].Path+"/"+out[j].Name)
	})
	return out, nil
}

// Retry puts one failed entry back in the queue.
func (s *Service) Retry(vault, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.entry(vault, id)
	if err != nil {
		return err
	}
	// A newer remote version that failed to download is tracked by remote ID, not on the entry.
	if _, ok := s.remoteFail[e.RemoteID]; ok && e.RemoteID != "" {
		delete(s.remoteFail, e.RemoteID)
		s.notify()
	}
	if e.SyncError == "" {
		return nil
	}
	clearFailure(&e)
	return s.saveEntry(e)
}

// RetryAll forgets every recorded failure, so the next round tries everything again.
// Used by the manual sync button and once after the program is upgraded.
func (s *Service) RetryAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remoteFail = nil
	changed := false
	for _, e := range s.entries {
		if e.SyncError == "" {
			continue
		}
		clearFailure(&e)
		if err := s.put("entry", e.ID, e); err != nil {
			return err
		}
		s.entries[e.ID] = e
		changed = true
	}
	if changed {
		s.notify()
	}
	return nil
}

// ResetFailuresOnce runs RetryAll the first time it sees tag (the program version): a newer build
// may be able to send what an older one could not.
func (s *Service) ResetFailuresOnce(tag string) error {
	if tag == "" {
		return nil
	}
	var seen string
	s.mu.Lock()
	err := s.db.QueryRow(`SELECT value FROM records WHERE kind='meta' AND id='failureReset'`).Scan(&seen)
	s.mu.Unlock()
	if err == nil && seen == `"`+tag+`"` {
		return nil
	}
	if err = s.RetryAll(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put("meta", "failureReset", tag)
}
