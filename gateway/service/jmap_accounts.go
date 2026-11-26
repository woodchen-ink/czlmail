package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// UpstreamAccounts lists a gateway user's upstream mailboxes as JMAP accounts.
// No upstream exists yet, so every user gets an empty list and the Session
// answers 403 no-upstream; the upstream work packages fill this in.
type UpstreamAccounts struct {
	db *gorm.DB
}

// NewUpstreamAccounts returns the account source backed by db.
func NewUpstreamAccounts(db *gorm.DB) *UpstreamAccounts { return &UpstreamAccounts{db: db} }

// UserSession implements jmapserver.AccountSource. The username is the user's email.
func (a *UpstreamAccounts) UserSession(ctx context.Context, userPublicID string) (*jmapserver.UserSession, error) {
	var u model.User
	err := a.db.WithContext(ctx).Select("email").Where("public_id = ?", userPublicID).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Deleted between authentication and now: nothing to show.
		return &jmapserver.UserSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &jmapserver.UserSession{Username: u.Email}, nil
}
