package service

import (
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// Publish pushes new states of an account that a JMAP method changed (writes
// outside the runner's sync rounds). Unknown or stopped accounts are ignored.
func (m *UpstreamManager) Publish(accountID string, states map[string]string) {
	if len(states) == 0 {
		return
	}
	m.mu.Lock()
	r := m.running[accountID]
	n := m.notifier
	m.mu.Unlock()
	userID := ""
	if r != nil {
		userID = r.userID
	} else {
		var row struct{ UserID uint }
		if m.db.Table("upstreams").Select("user_id").Where("public_id = ?", accountID).Scan(&row).Error != nil || row.UserID == 0 {
			return
		}
		id, err := m.userPublicID(row.UserID)
		if err != nil {
			return
		}
		userID = id
	}
	if n != nil {
		n.Publish(userID, jmapserver.Changes{accountID: states})
	}
}

// Fail hands an upstream failure a JMAP method met (an SMTP 535) to the
// account's runner, which then reports it like its own (authRequired).
func (m *UpstreamManager) Fail(accountID string, err error) {
	m.mu.Lock()
	r := m.running[accountID]
	m.mu.Unlock()
	if r != nil {
		r.runner.Fail(err)
	}
}
