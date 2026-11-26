package service

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Admin session lifetime: signed out after SessionIdle without requests, and after
// SessionMaxAge in any case.
const (
	SessionIdle   = 7 * 24 * time.Hour
	SessionMaxAge = 30 * 24 * time.Hour
	sessionTouch  = time.Minute // LastSeenAt is written at most this often
)

// AdminPrincipal is the signed-in user of an admin request.
type AdminPrincipal struct {
	User      model.User
	SessionID uint
}

func (a *Accounts) createSession(tx *gorm.DB, u *model.User, ip, userAgent string) (string, error) {
	token := newToken(32)
	now := a.now().UTC()
	s := model.AdminSession{
		TokenHash:  tokenHash(token),
		UserID:     u.ID,
		IP:         ip,
		UserAgent:  truncate(userAgent, 256),
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionMaxAge),
	}
	if err := tx.Create(&s).Error; err != nil {
		return "", err
	}
	return token, tx.Model(u).Update("last_login_at", now).Error
}

// ResolveSession returns the user signed in with the session cookie value, or
// ErrUnauthenticated when the session is unknown, expired, idle or its user is disabled.
func (a *Accounts) ResolveSession(token string) (*AdminPrincipal, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	var s model.AdminSession
	err := a.db.Where("token_hash = ?", tokenHash(token)).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	now := a.now().UTC()
	if !now.Before(s.ExpiresAt) || now.Sub(s.LastSeenAt) >= SessionIdle {
		a.db.Delete(&s)
		return nil, ErrUnauthenticated
	}
	var u model.User
	if err := a.db.First(&u, s.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUnauthenticated
		}
		return nil, err
	}
	if u.Disabled {
		return nil, ErrUnauthenticated
	}
	if now.Sub(s.LastSeenAt) >= sessionTouch {
		a.db.Model(&s).Update("last_seen_at", now)
	}
	return &AdminPrincipal{User: u, SessionID: s.ID}, nil
}

// Logout ends one admin session.
func (a *Accounts) Logout(p *AdminPrincipal) error {
	return a.db.Delete(&model.AdminSession{}, p.SessionID).Error
}

// PruneSessions deletes expired and idle sessions.
func (a *Accounts) PruneSessions() (int64, error) {
	now := a.now().UTC()
	res := a.db.Where("expires_at <= ? OR last_seen_at <= ?", now, now.Add(-SessionIdle)).Delete(&model.AdminSession{})
	return res.RowsAffected, res.Error
}

func endSessions(tx *gorm.DB, userID uint, exceptSessionID uint) error {
	return tx.Where("user_id = ? AND id <> ?", userID, exceptSessionID).Delete(&model.AdminSession{}).Error
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
