package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 退出登录时可能还有一次恢复连接在认证途中 (离线退避、启动恢复)。它回来时必须放弃,
// 否则凭据已删的登录会带着内存里的旧凭据接着同步。
func TestConnectAfterSignOutGivesUp(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	si := &signIn{id: "s2", cfg: config.Config{ServerHost: "b", SessionEndpoint: "https://b/jmap", AuthMethod: config.AuthPassword}}
	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil)), store: st, ready: make(chan struct{})}
	a.markReady()
	a.extra = []*signIn{si}
	srv := fakeSessionServer(t, func() {
		a.mu.Lock()
		si.signedOut = true
		a.mu.Unlock()
	})

	cfg := config.Config{SessionEndpoint: srv.URL, AuthMethod: config.AuthPassword, Username: "me@b"}
	if err := a.connect(si, cfg, basicAuthClient("me@b", "pw"), nil, nil); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("restore after sign-out = %v, want errNotSignedIn", err)
	}
	if si.client != nil || si.sync != nil || !si.signedOut {
		t.Fatalf("signed-out sign-in got a session back: client=%v signedOut=%v", si.client != nil, si.signedOut)
	}

}

// 已退出的登录凭据本来就删了: 退避重试里读不到凭据不能当成登录失效弹提示。
func TestRequireSignInIgnoresSignedOut(t *testing.T) {
	a := &App{ctx: context.Background(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	si := &signIn{id: "s2", signedOut: true}
	a.extra = []*signIn{si}
	a.requireSignIn(si, nil, errNoStoredCredential)
	if si.reason != "" {
		t.Fatalf("signed-out sign-in marked as sign-in required: %q", si.reason)
	}
}

// 正在添加、还没写进配置的登录被退出时, 不能把它当成已退出的登录写进配置。
func TestSignOutOfUnsavedSignInWritesNothing(t *testing.T) {
	signInTestConfig(t)
	if err := config.UpdateFile(func(f *config.File) error {
		f.Config = config.Config{ServerHost: "one", SessionEndpoint: "https://one/jmap", AuthMethod: config.AuthPassword}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.primary.cfg = config.Config{ServerHost: "one"}
	si := &signIn{id: "s2"}
	a.extra = []*signIn{si}
	if err := a.signOutOf(si); err != nil {
		t.Fatal(err)
	}
	f, _ := config.LoadFile()
	if len(f.SignIns) != 0 {
		t.Fatalf("unsaved sign-in written to config: %+v", f.SignIns)
	}
}

// 主登录已退出、其余登录都移除后回到旧版退出登录的状态: 配置清空, 界面回登录页。
func TestRemoveLastSignInClearsSignedOutPrimary(t *testing.T) {
	signInTestConfig(t)
	primary := config.Config{ServerHost: "one", SessionEndpoint: "https://one/jmap", AuthMethod: config.AuthPassword, SignedOut: true}
	if err := config.UpdateFile(func(f *config.File) error {
		f.Config = primary
		f.AddSignIn(config.Config{ServerHost: "two", SessionEndpoint: "https://two/jmap", AuthMethod: config.AuthPassword})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 不给 ctx: emit 在测试里不发事件。
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.primary.cfg, a.primary.signedOut = primary, true
	a.extra = []*signIn{{id: "s2", cfg: config.Config{ServerHost: "two"}}}

	if err := a.RemoveSignIn("s2"); err != nil {
		t.Fatal(err)
	}
	f, _ := config.LoadFile()
	if f.Config.Configured() || f.Config.ServerHost != "" || len(f.SignIns) != 0 {
		t.Fatalf("config after removing the last other sign-in: %+v", f)
	}
	if a.primary.cfg.ServerHost != "" || a.primary.signedOut {
		t.Fatalf("primary in memory: %+v signedOut=%v", a.primary.cfg, a.primary.signedOut)
	}

	// 主登录没退出时不动。
	a.primary.cfg = config.Config{ServerHost: "one"}
	if done, err := a.settleSignedOutPrimary(); done || err != nil || a.primary.cfg.ServerHost != "one" {
		t.Fatalf("settle with a signed-in primary: %v %v", done, err)
	}
}
