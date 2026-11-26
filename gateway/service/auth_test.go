package service

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/model"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	acc   *Accounts
	basic *BasicAuthenticator
	inval *Invalidations
	sets  *Settings
	clock *fakeClock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close(db) })
	clock := &fakeClock{t: time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)}
	sets, err := LoadSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	lim := NewRateLimiter()
	lim.now = clock.now
	inval := NewInvalidations()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	acc := NewAccounts(db, sets, lim, inval, log)
	acc.now = clock.now
	basic := NewBasicAuthenticator(db, lim, inval, func(r *http.Request) string { return "198.51.100.7" }, log)
	basic.now = clock.now
	return &env{acc: acc, basic: basic, inval: inval, sets: sets, clock: clock}
}

func (e *env) owner(t *testing.T) (*model.User, string) {
	t.Helper()
	token, err := e.acc.IssueSetupToken()
	if err != nil || token == "" {
		t.Fatalf("issue setup token: %q %v", token, err)
	}
	u, session, err := e.acc.CompleteSetup(token, NewUser{Email: "owner@example.com", Name: "Owner", Password: "correct horse battery"}, "203.0.113.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	return u, session
}

func basicReq(user, pass string) *http.Request {
	r := httptest.NewRequest("GET", "/jmap/session", nil)
	r.SetBasicAuth(user, pass)
	return r
}

func TestSetupTokenOneTimeAndExpiry(t *testing.T) {
	e := newEnv(t)
	in := NewUser{Email: "owner@example.com", Name: "Owner", Password: "correct horse battery"}

	token, _ := e.acc.IssueSetupToken()
	e.clock.add(SetupTokenTTL)
	if _, _, err := e.acc.CompleteSetup(token, in, "ip", "ua"); !errors.Is(err, ErrInvalidSetupToken) {
		t.Fatalf("expired token: %v", err)
	}

	token, _ = e.acc.IssueSetupToken()
	if _, _, err := e.acc.CompleteSetup("wrong", in, "ip", "ua"); !errors.Is(err, ErrInvalidSetupToken) {
		t.Fatalf("wrong token: %v", err)
	}
	u, _, err := e.acc.CompleteSetup(token, in, "ip", "ua")
	if err != nil || u.Role != model.RoleOwner {
		t.Fatalf("setup: %+v %v", u, err)
	}
	in.Email = "second@example.com"
	if _, _, err := e.acc.CompleteSetup(token, in, "ip", "ua"); !errors.Is(err, ErrSetupCompleted) {
		t.Fatalf("reuse: %v", err)
	}
	if again, _ := e.acc.IssueSetupToken(); again != "" {
		t.Fatal("setup token issued after the owner exists")
	}
}

func TestLoginWrongPasswordAndRateLimit(t *testing.T) {
	e := newEnv(t)
	e.owner(t)
	for i := range LimitLoginUser.Burst {
		if _, _, err := e.acc.Login("owner@example.com", "wrong password", "203.0.113.1", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, _, err := e.acc.Login("owner@example.com", "correct horse battery", "203.0.113.2", "")
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfterSeconds() != 60 {
		t.Fatalf("after %d failures: %v", LimitLoginUser.Burst, err)
	}
	e.clock.add(time.Minute)
	if _, _, err := e.acc.Login("owner@example.com", "correct horse battery", "203.0.113.2", ""); err != nil {
		t.Fatalf("after waiting: %v", err)
	}
	// Successes do not use up the limit.
	for range LimitLoginUser.Burst + 5 {
		if _, _, err := e.acc.Login("owner@example.com", "correct horse battery", "203.0.113.2", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppPasswordShownOnceAndRevokedAtOnce(t *testing.T) {
	e := newEnv(t)
	_, session := e.owner(t)
	p, err := e.acc.ResolveSession(session)
	if err != nil {
		t.Fatal(err)
	}
	var events []UserInvalidation
	e.inval.Subscribe(func(ev UserInvalidation) { events = append(events, ev) })

	ap, plain, err := e.acc.CreateAppPassword(p, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "czl_"+ap.Prefix+"_") || len(plain) != 4+8+1+32 {
		t.Fatalf("app password %q", plain)
	}
	if strings.Contains(ap.Hash, plain) || !strings.HasPrefix(ap.Hash, "$argon2id$") {
		t.Fatal("stored value is not a hash")
	}

	id, err := e.basic.Authenticate(basicReq("Owner@Example.com", plain))
	if err != nil || id != p.User.PublicID {
		t.Fatalf("basic: %q %v", id, err)
	}
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", "correct horse battery")); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("admin password accepted for Basic: %v", err)
	}
	if _, err := e.basic.Authenticate(basicReq("other@example.com", plain)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("app password accepted for another username: %v", err)
	}

	list, _ := e.acc.ListAppPasswords(p)
	if len(list) != 1 || list[0].LastUsedAt == nil || list[0].LastUsedIP != "198.51.100.7" {
		t.Fatalf("list %+v", list)
	}

	if err := e.acc.RevokeAppPassword(p, ap.PublicID); err != nil {
		t.Fatal(err)
	}
	// The credential is cached, but revocation clears it.
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked password still works: %v", err)
	}
	if len(events) != 1 || events[0].AppPasswordID != ap.PublicID || events[0].UserPublicID != p.User.PublicID {
		t.Fatalf("events %+v", events)
	}
	if err := e.acc.RevokeAppPassword(p, ap.PublicID); !errors.Is(err, ErrAppPasswordNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
}

func TestBasicCacheExpires(t *testing.T) {
	e := newEnv(t)
	_, session := e.owner(t)
	p, _ := e.acc.ResolveSession(session)
	ap, plain, _ := e.acc.CreateAppPassword(p, "Phone")
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); err != nil {
		t.Fatal(err)
	}
	// Delete the row behind the authenticator's back: the cache answers until it expires.
	e.acc.db.Delete(&model.AppPassword{}, ap.ID)
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); err != nil {
		t.Fatalf("within the cache window: %v", err)
	}
	e.clock.add(basicCacheTTL)
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after the cache window: %v", err)
	}
}

func TestBasicRateLimitAndRepeatedFailure(t *testing.T) {
	e := newEnv(t)
	_, session := e.owner(t)
	p, _ := e.acc.ResolveSession(session)
	_, plain, _ := e.acc.CreateAppPassword(p, "Phone")
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); err != nil {
		t.Fatal(err)
	}

	// A device stuck on one stale password does not use up the limit.
	stale := "czl_aaaaaaaa_" + strings.Repeat("b", 32)
	for range 3 * LimitBasicUser.Burst {
		if _, err := e.basic.Authenticate(basicReq("owner@example.com", stale)); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("stale password: %v", err)
		}
	}
	// Guessing different passwords does.
	var err error
	for i := range LimitBasicUser.Burst + 1 {
		_, err = e.basic.Authenticate(basicReq("owner@example.com", "guess-"+string(rune('a'+i))))
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("after guesses: %v", err)
	}
	// Authenticated (cached) requests are not limited.
	if _, err := e.basic.Authenticate(basicReq("owner@example.com", plain)); err != nil {
		t.Fatalf("valid cached credential limited: %v", err)
	}
}

func TestDisableUserEndsSessionsAndBasic(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.owner(t)
	u, err := e.acc.CreateUser(owner, NewUser{Email: "user@example.com", Name: "User", Password: "user password 1", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := e.acc.Login("user@example.com", "user password 1", "ip", "")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.acc.ResolveSession(session)
	_, plain, _ := e.acc.CreateAppPassword(p, "Phone")
	if _, err := e.basic.Authenticate(basicReq("user@example.com", plain)); err != nil {
		t.Fatal(err)
	}
	var events []UserInvalidation
	e.inval.Subscribe(func(ev UserInvalidation) { events = append(events, ev) })

	yes := true
	if _, err := e.acc.UpdateUser(owner, u.PublicID, UserPatch{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.acc.ResolveSession(session); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session after disable: %v", err)
	}
	if _, err := e.basic.Authenticate(basicReq("user@example.com", plain)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("basic after disable: %v", err)
	}
	if _, _, err := e.acc.Login("user@example.com", "user password 1", "ip", ""); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("login after disable: %v", err)
	}
	if len(events) != 1 || events[0].Reason != "disabled" || events[0].AppPasswordID != "" {
		t.Fatalf("events %+v", events)
	}

	no := false
	if _, err := e.acc.UpdateUser(owner, u.PublicID, UserPatch{Disabled: &no}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.basic.Authenticate(basicReq("user@example.com", plain)); err != nil {
		t.Fatalf("basic after re-enable: %v", err)
	}
}

func TestRolePermissions(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.owner(t)
	admin, err := e.acc.CreateUser(owner, NewUser{Email: "admin@example.com", Name: "A", Password: "admin password", Role: model.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	user, _ := e.acc.CreateUser(admin, NewUser{Email: "u@example.com", Name: "U", Password: "user password", Role: model.RoleUser})
	if user == nil {
		t.Fatal("admin cannot create a user")
	}
	if _, err := e.acc.CreateUser(admin, NewUser{Email: "a2@example.com", Name: "A2", Password: "admin password", Role: model.RoleAdmin}); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin created an admin: %v", err)
	}
	yes := true
	if _, err := e.acc.UpdateUser(admin, owner.PublicID, UserPatch{Disabled: &yes}); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin disabled the owner: %v", err)
	}
	if _, err := e.acc.UpdateUser(owner, owner.PublicID, UserPatch{Disabled: &yes}); !errors.Is(err, ErrSelfModify) {
		t.Errorf("owner disabled themselves: %v", err)
	}
	if err := e.acc.DeleteUser(owner, owner.PublicID); !errors.Is(err, ErrSelfModify) {
		t.Errorf("owner deleted themselves: %v", err)
	}
	if _, err := e.acc.ListUsers(user); !errors.Is(err, ErrForbidden) {
		t.Errorf("user listed users: %v", err)
	}
	if _, err := e.acc.CreateUser(owner, NewUser{Email: "u@example.com", Name: "dup", Password: "user password", Role: model.RoleUser}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("duplicate email: %v", err)
	}
	if err := e.acc.DeleteUser(admin, user.PublicID); err != nil {
		t.Errorf("admin deleting a user: %v", err)
	}
}

func TestRegistrationClosedByDefault(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.owner(t)
	in := NewUser{Email: "new@example.com", Name: "New", Password: "new user password"}
	if _, _, err := e.acc.Register(in, "ip", ""); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed: %v", err)
	}
	if e.sets.Bool(SettingAllowPrivateNetworks) {
		t.Fatal("private networks allowed by default")
	}
	err := e.sets.Update(owner, map[string]json.RawMessage{SettingRegistrationOpen: json.RawMessage("true")})
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := e.acc.Register(in, "ip", "")
	if err != nil || u.Role != model.RoleUser {
		t.Fatalf("open: %+v %v", u, err)
	}
	var se *SettingError
	err = e.sets.Update(owner, map[string]json.RawMessage{SettingRegistrationOpen: json.RawMessage(`"yes"`)})
	if !errors.As(err, &se) || !errors.Is(err, ErrInvalidSetting) || se.Key != SettingRegistrationOpen {
		t.Fatalf("bad value: %v", err)
	}
	if err := e.sets.Update(u, map[string]json.RawMessage{SettingRegistrationOpen: json.RawMessage("false")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("user changed a setting: %v", err)
	}
}
