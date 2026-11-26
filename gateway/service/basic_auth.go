package service

import (
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
)

// Basic credential cache: a verified username + app password skips argon2id for
// basicCacheTTL; a failed one is answered without argon2id and without counting
// against the rate limit for the same time, so a device left with a revoked password
// does not lock its user out. Revocation, disabling and deleting clear entries at once.
const (
	basicCacheTTL  = 5 * time.Minute
	basicCacheMax  = 10000
	lastUsedWrites = time.Minute // LastUsedAt is written at most this often per app password
)

// Credential identifies who a Basic request authenticated as.
type Credential struct {
	UserPublicID  string
	AppPasswordID string // public id of the app password used
}

// BasicAuthenticator checks JMAP Basic credentials: the user's email and an app password.
// The admin password is never accepted.
type BasicAuthenticator struct {
	db       *gorm.DB
	limiter  *RateLimiter
	clientIP func(*http.Request) string
	log      *slog.Logger
	now      func() time.Time

	mu   sync.Mutex
	gen  uint64 // bumped by every invalidation; a check started before one is not cached
	good map[[32]byte]*goodEntry
	bad  map[[32]byte]badEntry
}

type goodEntry struct {
	cred      Credential
	appPassID uint
	expires   time.Time
	touchedAt time.Time
	touchedIP string
}

type badEntry struct {
	username string
	expires  time.Time
}

// NewBasicAuthenticator returns an authenticator that forgets credentials as soon as
// inval reports them revoked.
func NewBasicAuthenticator(db *gorm.DB, limiter *RateLimiter, inval *Invalidations, clientIP func(*http.Request) string, log *slog.Logger) *BasicAuthenticator {
	a := &BasicAuthenticator{
		db: db, limiter: limiter, clientIP: clientIP, log: log, now: time.Now,
		good: map[[32]byte]*goodEntry{}, bad: map[[32]byte]badEntry{},
	}
	inval.Subscribe(a.forget)
	inval.onUserChanged(func(string) { a.forgetFailures() })
	return a
}

// Authenticate returns the public id of the user the request's Basic credentials belong
// to. Errors are ErrUnauthenticated (answer 401) or *RateLimitError (answer 429).
func (a *BasicAuthenticator) Authenticate(r *http.Request) (string, error) {
	c, err := a.AuthenticateCredential(r)
	return c.UserPublicID, err
}

// AuthenticateCredential is Authenticate that also names the app password used.
func (a *BasicAuthenticator) AuthenticateCredential(r *http.Request) (Credential, error) {
	username, password, ok := r.BasicAuth()
	if !ok || username == "" || password == "" || len(username) > 254 || len(password) > 256 {
		return Credential{}, ErrUnauthenticated
	}
	username = NormalizeEmail(username)
	key := sha256.Sum256([]byte(username + "\x00" + password))
	ip := a.clientIP(r)
	now := a.now()

	a.mu.Lock()
	if e := a.good[key]; e != nil && now.Before(e.expires) {
		cred, touch := e.cred, now.Sub(e.touchedAt) >= lastUsedWrites || e.touchedIP != ip
		if touch {
			e.touchedAt, e.touchedIP = now, ip
		}
		id := e.appPassID
		a.mu.Unlock()
		if touch {
			a.touch(id, ip, now)
		}
		return cred, nil
	}
	if e, ok := a.bad[key]; ok && now.Before(e.expires) {
		a.mu.Unlock()
		return Credential{}, ErrUnauthenticated
	}
	gen := a.gen
	a.mu.Unlock()

	res, err := a.limiter.take(
		limitKey{"basic:user:" + username, LimitBasicUser},
		limitKey{"basic:ip:" + ip, LimitBasicIP},
	)
	if err != nil {
		return Credential{}, err
	}
	cred, appPassID, err := a.verify(username, password)
	if errors.Is(err, ErrUnauthenticated) {
		a.remember(key, gen, nil, badEntry{username: username, expires: now.Add(basicCacheTTL)})
		return Credential{}, err
	}
	if err != nil {
		res.refund()
		return Credential{}, err
	}
	res.refund()
	a.remember(key, gen, &goodEntry{cred: cred, appPassID: appPassID, expires: now.Add(basicCacheTTL), touchedAt: now, touchedIP: ip}, badEntry{})
	a.touch(appPassID, ip, now)
	return cred, nil
}

func (a *BasicAuthenticator) verify(username, password string) (Credential, uint, error) {
	prefix, ok := parseAppPassword(password)
	if !ok {
		return Credential{}, 0, ErrUnauthenticated
	}
	var ap model.AppPassword
	err := a.db.Where("prefix = ?", prefix).First(&ap).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		crypto.BurnPasswordCheck(password)
		return Credential{}, 0, ErrUnauthenticated
	}
	if err != nil {
		return Credential{}, 0, err
	}
	var u model.User
	if err := a.db.First(&u, ap.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Credential{}, 0, ErrUnauthenticated
		}
		return Credential{}, 0, err
	}
	if !checkPassword(password, ap.Hash) || u.Email != username || u.Disabled {
		return Credential{}, 0, ErrUnauthenticated
	}
	return Credential{UserPublicID: u.PublicID, AppPasswordID: ap.PublicID}, ap.ID, nil
}

// remember stores a result unless an invalidation happened since the check began.
func (a *BasicAuthenticator) remember(key [32]byte, gen uint64, good *goodEntry, bad badEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.gen {
		return
	}
	now := a.now()
	if good != nil {
		if len(a.good) >= basicCacheMax {
			sweepExpired(a.good, now, func(e *goodEntry) time.Time { return e.expires })
		}
		if len(a.good) < basicCacheMax {
			a.good[key] = good
		}
		delete(a.bad, key)
		return
	}
	if len(a.bad) >= basicCacheMax {
		sweepExpired(a.bad, now, func(e badEntry) time.Time { return e.expires })
	}
	if len(a.bad) < basicCacheMax {
		a.bad[key] = bad
	}
}

func sweepExpired[V any](m map[[32]byte]V, now time.Time, expires func(V) time.Time) {
	for k, v := range m {
		if !now.Before(expires(v)) {
			delete(m, k)
		}
	}
}

func (a *BasicAuthenticator) forget(ev UserInvalidation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	for k, e := range a.good {
		if e.cred.UserPublicID == ev.UserPublicID && (ev.AppPasswordID == "" || e.cred.AppPasswordID == ev.AppPasswordID) {
			delete(a.good, k)
		}
	}
}

// forgetFailures drops cached failures, so a re-enabled user's devices get through at once.
func (a *BasicAuthenticator) forgetFailures() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	clear(a.bad)
}

func (a *BasicAuthenticator) touch(appPassID uint, ip string, now time.Time) {
	err := a.db.Model(&model.AppPassword{}).Where("id = ?", appPassID).
		Updates(map[string]any{"last_used_at": now.UTC(), "last_used_ip": ip}).Error
	if err != nil {
		a.log.Warn("record app password use", "err", err)
	}
}
