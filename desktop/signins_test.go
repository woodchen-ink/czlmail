package main

import (
	"context"
	"errors"
	"log/slog"
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
