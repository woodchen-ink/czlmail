package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

func signInTestConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("CZLMAIL_INSTANCE", "signins-api-test")
}

func pw(endpoint, user string) config.Config {
	return config.Config{ServerHost: endpoint, AuthMethod: config.AuthPassword, SessionEndpoint: endpoint, Username: user}
}

// 连服务器之前: 新登录不能重复已有的身份, 重新登录附加登录不能换人; 主登录照旧可以换人 (缓存归属另有检查)。
func TestCheckSignInTarget(t *testing.T) {
	signInTestConfig(t)
	const one, two = "https://one/jmap", "https://two/jmap"
	if err := config.UpdateFile(func(f *config.File) error {
		f.Config = pw(one, "alice@one")
		f.AddSignIn(pw(two, "carol@two"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: context.Background()}
	extra := &signIn{id: "s2"}
	a.extra = []*signIn{extra}

	cases := []struct {
		name     string
		si       *signIn
		isNew    bool
		endpoint string
		user     string
		want     error
	}{
		{"new, same as primary", &signIn{id: "s3"}, true, one, "ALICE@one", errAlreadySignedIn},
		{"new, same as extra", &signIn{id: "s3"}, true, two, "carol@two", errAlreadySignedIn},
		{"new, other user on a known server", &signIn{id: "s3"}, true, one, "bob@one", nil},
		{"again, same user", extra, false, two, "Carol@two", nil},
		{"again, other user", extra, false, two, "dave@two", errDifferentAccount},
		{"again, primary as extra's user", &a.primary, false, two, "carol@two", errAlreadySignedIn},
		{"again, primary other user", &a.primary, false, one, "bob@one", nil},
	}
	for _, c := range cases {
		if err := a.checkSignInTarget(c.si, c.isNew, c.endpoint, c.user); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// 连上之后按服务器给的用户名再比一次: 浏览器授权事先不知道是谁, 用户名也可能是别名。
func TestAcceptIdentity(t *testing.T) {
	signInTestConfig(t)
	const one, two = "https://one/jmap", "https://two/jmap"
	if err := config.UpdateFile(func(f *config.File) error {
		f.Config = pw(one, "alice@one")
		f.AddSignIn(pw(two, "carol@two"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: context.Background()}
	a.primary.cfg = pw(one, "alice@one")
	extra := &signIn{id: "s2", cfg: pw(two, "carol@two")}
	a.extra = []*signIn{extra}

	fresh := &signIn{id: "s3"}
	if err := a.acceptIdentity(fresh, true, "")(pw(one, "Alice@one")); !errors.Is(err, errAlreadySignedIn) {
		t.Errorf("OAuth into the primary identity: %v", err)
	}
	if err := a.acceptIdentity(fresh, true, "")(pw(one, "bob@one")); err != nil {
		t.Errorf("new user: %v", err)
	}
	if err := a.acceptIdentity(extra, false, "")(pw(two, "dave@two")); !errors.Is(err, errDifferentAccount) {
		t.Errorf("re-auth as someone else: %v", err)
	}
	// 用户输入的是别名, 服务器回的是正式地址: 配置里存的是输入的写法, 两者有一个对得上即可。
	if err := a.acceptIdentity(extra, false, "carol@two")(pw(two, "carol.real@two")); err != nil {
		t.Errorf("re-auth through an alias: %v", err)
	}
}

// 退出一个登录: 别的登录照常, 谁的缓存都不删; 配置留着并标上 SignedOut, 启动时不连。
// 主登录在还有别的登录时也这样; 只剩主登录时照旧清掉配置 (界面回到登录页)。
func TestSignOutOfKeepsOtherSignIns(t *testing.T) {
	signInTestConfig(t)
	// 配置里不写用户名与 issuer: 测试不碰系统钥匙串。
	if err := config.UpdateFile(func(f *config.File) error {
		f.Config = config.Config{ServerHost: "one", SessionEndpoint: "https://one/jmap", AuthMethod: config.AuthPassword}
		f.AddSignIn(config.Config{ServerHost: "two", SessionEndpoint: "https://two/jmap", AuthMethod: config.AuthPassword})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	primaryStopped, extraStopped := false, false
	a.primary.cfg = config.Config{ServerHost: "one"}
	a.primary.sync, a.primary.cancelSync = &syncer.Syncer{}, func() { primaryStopped = true }
	extra := &signIn{id: "s2", cfg: config.Config{ServerHost: "two"}, sync: &syncer.Syncer{}, cancelSync: func() { extraStopped = true }}
	a.extra = []*signIn{extra}

	if err := a.signOutOf(extra); err != nil {
		t.Fatal(err)
	}
	if !extraStopped || primaryStopped || !extra.signedOut || a.primary.sync == nil || len(a.extra) != 1 {
		t.Fatalf("sign out of s2: extra stopped=%v primary stopped=%v signedOut=%v", extraStopped, primaryStopped, extra.signedOut)
	}
	f, _ := config.LoadFile()
	if c, ok := f.SignIn("s2"); !ok || !c.SignedOut || c.ServerHost != "two" {
		t.Fatalf("s2 config after sign-out: %+v %v", c, ok)
	}

	if err := a.signOutOf(&a.primary); err != nil {
		t.Fatal(err)
	}
	f, _ = config.LoadFile()
	if !primaryStopped || !f.Config.SignedOut || f.Config.ServerHost != "one" || len(f.SignIns) != 1 {
		t.Fatalf("primary sign-out with another sign-in left: %+v", f)
	}

	// 只剩主登录: 配置清空。
	a.extra = nil
	if err := config.UpdateFile(func(f *config.File) error { f.SignIns = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.signOutOf(&a.primary); err != nil {
		t.Fatal(err)
	}
	f, _ = config.LoadFile()
	if f.Config.Configured() || f.Config.ServerHost != "" || a.primary.signedOut {
		t.Fatalf("primary sign-out alone should clear the config: %+v", f.Config)
	}
}

// 主登录换了人: 还有别的登录时只清主登录的缓存与设置条目, 别的登录的不动。
func TestEnsureCacheOwnerKeepsOtherSignIns(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertAccounts(ctx, tx, []store.Account{{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}}, nil); err != nil {
			return err
		}
		if err := store.UpsertEmails(ctx, tx, "c", []store.Email{{ID: "m1", Subject: "old primary"}}); err != nil {
			return err
		}
		return store.UpsertEmails(ctx, tx, "s2~c", []store.Email{{ID: "m1", Subject: "second"}})
	}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		store.SettingCacheOwner:                  "https://one/jmap\x00old@one",
		"mutedMailAccounts":                      `["c","s2~c"]`,
		"reconciledAt:s2~c":                      "2026-11-24T00:00:00Z",
		accountLookPrefix + "https://one/jmap#c": `{"name":"Old"}`,
	} {
		if err := st.SetStringSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.extra = []*signIn{{id: "s2"}}
	if err := a.ensureCacheOwner(ctx, st, config.Config{SessionEndpoint: "https://one/jmap", Username: "new@one"}, sessionWith("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Email(ctx, "c", "m1"); err == nil {
		t.Error("the previous primary user's mail should be gone")
	}
	if e, err := st.Email(ctx, "s2~c", "m1"); err != nil || e.Subject != "second" {
		t.Errorf("the other sign-in's mail must stay: %+v %v", e, err)
	}
	if v, _ := st.StringSetting(ctx, "mutedMailAccounts"); v != `["s2~c"]` {
		t.Errorf("muted = %s", v)
	}
	if v, _ := st.StringSetting(ctx, "reconciledAt:s2~c"); v == "" {
		t.Error("reconcile mark of the other sign-in was cleared")
	}
	if looks, _ := st.TimedSettingsWithPrefix(ctx, accountLookPrefix); len(looks) != 0 {
		t.Errorf("account looks of the previous primary user's drive stayed: %v", looks)
	}
}
