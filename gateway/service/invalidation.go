package service

import (
	"slices"
	"sync"
)

// UserInvalidation tells listeners that credentials of a user stopped working:
// the user was disabled, deleted or renamed, or one app password was revoked.
// The JMAP server closes the user's EventSource connections and drops cached sessions;
// clients holding a still-valid app password reconnect and authenticate again.
type UserInvalidation struct {
	UserPublicID string
	// AppPasswordID is the revoked app password's public id; empty when every
	// credential of the user is affected.
	AppPasswordID string
	Reason        string // "disabled", "deleted", "email_changed", "app_password_revoked"
}

// Invalidations fans out user changes. Listeners run synchronously after the change
// is committed and must not block.
type Invalidations struct {
	mu      sync.RWMutex
	revoked []func(UserInvalidation)
	changed []func(userPublicID string)
}

// NewInvalidations returns a hub without listeners.
func NewInvalidations() *Invalidations { return &Invalidations{} }

// Subscribe registers fn for every UserInvalidation.
func (h *Invalidations) Subscribe(fn func(UserInvalidation)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revoked = append(h.revoked, fn)
}

// onUserChanged registers fn for any change to a user row, including ones that make
// credentials valid again (re-enabling); the Basic cache uses it to forget failures.
func (h *Invalidations) onUserChanged(fn func(userPublicID string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changed = append(h.changed, fn)
}

func (h *Invalidations) publish(ev UserInvalidation) {
	h.mu.RLock()
	fns := slices.Clone(h.revoked)
	h.mu.RUnlock()
	for _, fn := range fns {
		fn(ev)
	}
}

func (h *Invalidations) userChanged(userPublicID string) {
	h.mu.RLock()
	fns := slices.Clone(h.changed)
	h.mu.RUnlock()
	for _, fn := range fns {
		fn(userPublicID)
	}
}
