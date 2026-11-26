package service

import (
	"errors"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
)

// Login checks an admin email + password and opens a session. App passwords are not
// accepted here, and the admin password is not accepted for JMAP Basic.
func (a *Accounts) Login(email, password, ip, userAgent string) (*model.User, string, error) {
	res, err := a.limiter.take(
		limitKey{"login:user:" + email, LimitLoginUser},
		limitKey{"login:ip:" + ip, LimitLoginIP},
	)
	if err != nil {
		return nil, "", err
	}
	u, err := a.userByEmail(a.db, email)
	if errors.Is(err, ErrUserNotFound) {
		crypto.BurnPasswordCheck(password)
		return nil, "", ErrInvalidCredentials
	}
	if err != nil {
		res.refund()
		return nil, "", err
	}
	if !checkPassword(password, u.PasswordHash) {
		return nil, "", ErrInvalidCredentials
	}
	res.refund()
	if u.Disabled {
		return nil, "", ErrAccountDisabled
	}
	token, err := a.createSession(a.db, u, ip, userAgent)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// Register creates a user account when open registration is on, and signs it in.
func (a *Accounts) Register(in NewUser, ip, userAgent string) (*model.User, string, error) {
	if !a.settings.Bool(SettingRegistrationOpen) {
		return nil, "", ErrRegistrationClosed
	}
	if _, err := a.limiter.take(limitKey{"signup:ip:" + ip, LimitSignupIP}); err != nil {
		return nil, "", err
	}
	var u *model.User
	var token string
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		if u, err = a.insertUser(tx, in, model.RoleUser); err != nil {
			return err
		}
		token, err = a.createSession(tx, u, ip, userAgent)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// ChangeOwnPassword replaces the signed-in user's admin password and signs out their
// other sessions. App passwords keep working.
func (a *Accounts) ChangeOwnPassword(p *AdminPrincipal, current, next string) error {
	res, err := a.limiter.take(limitKey{"login:user:" + p.User.Email, LimitLoginUser})
	if err != nil {
		return err
	}
	if !checkPassword(current, p.User.PasswordHash) {
		return ErrWrongPassword
	}
	res.refund()
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", p.User.ID).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return endSessions(tx, p.User.ID, p.SessionID)
	})
}
