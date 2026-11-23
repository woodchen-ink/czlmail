package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// sessionTestApp 组装一个主登录已连上 client 的 App, 供集成测试用。
func sessionTestApp(ctx context.Context, log *slog.Logger, st *store.Store, client *jmap.Client) *App {
	a := &App{ctx: ctx, log: log, store: st}
	a.primary.client = client
	a.primary.sync = syncer.New(client, st, log)
	return a
}

// 主登录的账号 id 原样交给服务器; 带前缀但没有对应登录的账号 id 按未登录处理, 不落到主登录上。
func TestRouteResolvesSignInAndServerID(t *testing.T) {
	a := &App{ctx: context.Background()}
	if _, err := a.route("c"); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("未连上时应为未登录, 实际 %v", err)
	}

	client := &jmap.Client{}
	a.primary.client = client
	a.primary.sync = &syncer.Syncer{}
	a.primary.cfg = config.Config{Username: "wood@example"}

	rt, err := a.route("c")
	if err != nil || rt.signIn != &a.primary || rt.client != client || rt.sync != a.primary.sync || rt.serverID != "c" {
		t.Fatalf("route(c) = %+v, %v", rt, err)
	}
	if _, err := a.route("s2~c"); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("不存在的登录应为未登录, 实际 %v", err)
	}
	if got := a.usernameFor("c"); got != "wood@example" {
		t.Errorf("usernameFor(c) = %q", got)
	}
	if got := a.usernameFor("s2~c"); got != "" {
		t.Errorf("usernameFor(s2~c) = %q, 应为空", got)
	}
}

// 会话正在恢复时 route 等它连上, 而不是立刻报未登录。
func TestRouteWaitsForRestore(t *testing.T) {
	a := &App{ctx: context.Background()}
	a.primary.restoring.Store(true)
	s := &syncer.Syncer{}
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.mu.Lock()
		a.primary.sync = s
		a.mu.Unlock()
		a.primary.restoring.Store(false)
	}()
	got, err := a.syncerFor("c")
	if err != nil || got != s {
		t.Fatalf("应等到恢复完成, 实际 %v, %v", got, err)
	}
	if !a.signedIn() {
		t.Error("连上后 signedIn 应为真")
	}

	// 离线退避的间隙里不等。
	b := &App{ctx: context.Background()}
	start := time.Now()
	if b.signedIn() {
		t.Error("没有会话时 signedIn 应为假")
	}
	if _, err := b.route("c"); !errors.Is(err, errNotSignedIn) || time.Since(start) > time.Second {
		t.Errorf("不在恢复中时应立刻报未登录, 实际 %v (%v)", err, time.Since(start))
	}
}

// 删掉附加登录: 只删它自己的账号缓存与设置条目, 主登录和别的登录不动; 配置里删掉它但前缀序号不回退。
func TestRemoveSignIn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("CZLMAIL_INSTANCE", "signin-test")
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, []store.Account{
			{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}, {ID: "s2~e"}, {ID: "s3~x", IsPersonal: true},
		}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"mutedMailAccounts":    `["c","s2~c"]`,
		settingUnifiedExcluded: `["s2~e","s3~x"]`,
		"defaultIdentities":    `{"c":"i1","s2~c":"i2"}`,
		"reconciledAt:s2~c":    "2026-11-23T00:00:00Z",
	} {
		if err := st.SetStringSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	// 配置里的附加登录不带凭据信息, 测试不碰系统钥匙串。
	if err := config.UpdateFile(func(f *config.File) error {
		f.AddSignIn(config.Config{ServerHost: "b"})
		f.AddSignIn(config.Config{ServerHost: "c"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil)), store: st}
	cancelled := false
	a.extra = []*signIn{
		{id: "s2", sync: &syncer.Syncer{}, cancelSync: func() { cancelled = true }},
		{id: "s3"},
	}
	if err := a.dropSignIn("s2"); err != nil {
		t.Fatal(err)
	}
	if !cancelled || len(a.extra) != 1 || a.extra[0].id != "s3" {
		t.Fatalf("sign-in not stopped and dropped: cancelled=%v extra=%v", cancelled, a.extra)
	}
	if _, err := a.route("s2~c"); !errors.Is(err, errNotSignedIn) {
		t.Errorf("removed sign-in still routes: %v", err)
	}

	accs, _ := st.Accounts(ctx)
	var ids []string
	for _, acc := range accs {
		ids = append(ids, acc.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"c", "s3~x"}) {
		t.Errorf("accounts = %v", ids)
	}
	want := map[string]string{
		"mutedMailAccounts":    `["c"]`,
		settingUnifiedExcluded: `["s3~x"]`,
		"defaultIdentities":    `{"c":"i1"}`,
	}
	for k, v := range want {
		if got, _ := st.StringSetting(ctx, k); got != v {
			t.Errorf("%s = %s, want %s", k, got, v)
		}
	}
	if got, _ := st.StringSetting(ctx, "reconciledAt:s2~c"); got != "" {
		t.Errorf("reconcile mark left behind: %q", got)
	}
	f, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.SignIns) != 1 || f.SignIns[0].ID != "s3" || f.LastSignIn != 3 {
		t.Errorf("config = %+v", f)
	}
	if err := a.dropSignIn(""); err == nil {
		t.Error("the primary sign-in must not be removable here")
	}
}
