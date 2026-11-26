package imap

import (
	"time"
)

// Polling intervals per folder.
const (
	pollChanged   = time.Minute      // the folder changed at its last check
	pollNormal    = 3 * time.Minute  // other folders
	pollColdRoles = 10 * time.Minute // junk and trash
	pollIdle      = 15 * time.Minute // no push client for a while, or NOTIFY covers changes
	listEvery     = 15 * time.Minute // LIST for created / deleted folders without LIST-STATUS
)

// schedule tracks when each folder is due for a check. Guarded by Upstream.mu.
type schedule struct {
	due      map[string]time.Time // mailbox id → next check
	nextList time.Time
}

func (s *schedule) init() { s.due = map[string]time.Time{} }

// interval is how long until a folder is checked again.
func interval(role string, changed, idle, notify bool) time.Duration {
	switch {
	case idle || notify:
		return pollIdle
	case role == "junk" || role == "trash":
		return pollColdRoles
	case changed:
		return pollChanged
	}
	return pollNormal
}

func (u *Upstream) isDue(id string, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	t, ok := u.sched.due[id]
	return !ok || !now.Before(t)
}

func (u *Upstream) checked(id, role string, changed, idle bool, now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sched.due[id] = now.Add(interval(role, changed, idle, u.caps.notify))
}

func (u *Upstream) listDue(now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return !now.Before(u.sched.nextList)
}

func (u *Upstream) listed(now time.Time, idle bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	every := listEvery
	if !idle && u.caps.listStatus && !u.caps.notify {
		every = pollNormal // LIST-STATUS is the folder check itself
	}
	u.sched.nextList = now.Add(every)
}

func (u *Upstream) forget(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.sched.due, id)
}
