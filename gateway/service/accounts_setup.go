package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// SetupTokenTTL is how long the first-run link printed to the log stays valid.
const SetupTokenTTL = 30 * time.Minute

// setupToken lives in memory only: a restart prints a new link and voids the old one.
type setupToken struct {
	hash    [32]byte
	expires time.Time
	used    bool
}

// IssueSetupToken returns a new one-time setup token while no user exists, or ""
// once the owner has been created.
func (a *Accounts) IssueSetupToken() (string, error) {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	n, err := a.userCount(a.db)
	if err != nil || n > 0 {
		return "", err
	}
	token := newToken(32)
	a.setup = setupToken{hash: sha256.Sum256([]byte(token)), expires: a.now().Add(SetupTokenTTL)}
	return token, nil
}

// SetupRequired reports whether the gateway still has no user.
func (a *Accounts) SetupRequired() (bool, error) {
	n, err := a.userCount(a.db)
	return n == 0, err
}

// CompleteSetup creates the owner with the one-time token and signs them in.
func (a *Accounts) CompleteSetup(token string, in NewUser, ip, userAgent string) (*model.User, string, error) {
	res, err := a.limiter.take(limitKey{"setup:ip:" + ip, LimitSetupIP})
	if err != nil {
		return nil, "", err
	}
	a.setupMu.Lock()
	defer a.setupMu.Unlock()

	n, err := a.userCount(a.db)
	if err != nil {
		return nil, "", err
	}
	if n > 0 {
		return nil, "", ErrSetupCompleted
	}
	sum := sha256.Sum256([]byte(token))
	if a.setup.used || a.setup.expires.IsZero() || !a.now().Before(a.setup.expires) ||
		subtle.ConstantTimeCompare(sum[:], a.setup.hash[:]) != 1 {
		return nil, "", ErrInvalidSetupToken
	}

	var owner *model.User
	var session string
	err = a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		if owner, err = a.insertUser(tx, in, model.RoleOwner); err != nil {
			return err
		}
		session, err = a.createSession(tx, owner, ip, userAgent)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	a.setup.used = true
	res.refund()
	a.log.Info("gateway owner created", "user", owner.PublicID)
	return owner, session, nil
}
