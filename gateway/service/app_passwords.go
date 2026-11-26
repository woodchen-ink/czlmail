package service

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// App passwords look like czl_<prefix>_<secret>: 8 + 32 lower-case base32 characters
// (40 + 160 random bits). The prefix finds the row; the whole value is hashed.
const (
	appPasswordTag       = "czl_"
	appPasswordPrefixLen = 8
	appPasswordSecretLen = 32
)

var b32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func randomB32(chars int) string {
	b := make([]byte, chars*5/8)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

// parseAppPassword returns the prefix of a well-formed app password.
func parseAppPassword(s string) (string, bool) {
	rest, ok := strings.CutPrefix(s, appPasswordTag)
	if !ok {
		return "", false
	}
	prefix, secret, ok := strings.Cut(rest, "_")
	if !ok || len(prefix) != appPasswordPrefixLen || len(secret) != appPasswordSecretLen {
		return "", false
	}
	if _, err := b32.DecodeString(prefix); err != nil {
		return "", false
	}
	if _, err := b32.DecodeString(secret); err != nil {
		return "", false
	}
	return prefix, true
}

// DisplayPrefix is how an app password is shown after creation, e.g. "czl_ab12cd34_…".
func DisplayPrefix(prefix string) string { return appPasswordTag + prefix + "_" }

// ListAppPasswords returns the signed-in user's app passwords, newest first.
func (a *Accounts) ListAppPasswords(p *AdminPrincipal) ([]model.AppPassword, error) {
	var list []model.AppPassword
	err := a.db.Where("user_id = ?", p.User.ID).Order("id DESC").Find(&list).Error
	return list, err
}

// CreateAppPassword adds an app password for one device and returns it in full.
// The full value is never stored and cannot be shown again.
func (a *Accounts) CreateAppPassword(p *AdminPrincipal, name string) (*model.AppPassword, string, error) {
	for range 3 {
		prefix := randomB32(appPasswordPrefixLen)
		plain := appPasswordTag + prefix + "_" + randomB32(appPasswordSecretLen)
		hash, err := hashPassword(plain)
		if err != nil {
			return nil, "", err
		}
		ap := &model.AppPassword{PublicID: NewPublicID(), UserID: p.User.ID, Name: name, Prefix: prefix, Hash: hash}
		err = a.db.Create(ap).Error
		if errors.Is(err, gorm.ErrDuplicatedKey) || err != nil && strings.Contains(err.Error(), "UNIQUE") {
			continue // prefix collision, 1 in 2^40
		}
		if err != nil {
			return nil, "", err
		}
		return ap, plain, nil
	}
	return nil, "", errors.New("could not allocate a unique app password prefix")
}

// RevokeAppPassword deletes one of the signed-in user's app passwords. Clients using it
// fail authentication on their next request, cached or not.
func (a *Accounts) RevokeAppPassword(p *AdminPrincipal, publicID string) error {
	res := a.db.Where("public_id = ? AND user_id = ?", publicID, p.User.ID).Delete(&model.AppPassword{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAppPasswordNotFound
	}
	a.inval.publish(UserInvalidation{UserPublicID: p.User.PublicID, AppPasswordID: publicID, Reason: "app_password_revoked"})
	return nil
}
