package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// 多登录场景测试 (#15) 的公共部分: 用 internal/jmaptest 起内存 JMAP 服务器, App 走真实的登录、恢复与同步路径。
// 只换掉三样宿主能力: 配置目录 (临时目录)、发给界面的事件 (tap 记下来)、系统通知 (fakeNotifier 记下来)。
// 钥匙串是真的 (服务名由 TestMain 换成 czlmail-go-test), 测试结束时删掉写进去的密码。

// testApp 是一个跑着的 App 实例; restart 关掉它再用同一份配置与缓存库起一个新的, 模拟重启程序。
type testApp struct {
	t      *testing.T
	a      *App
	ctx    context.Context
	cancel context.CancelFunc
	db     string
	events *eventLog
	notes  *fakeNotifier
}

type eventLog struct {
	mu   sync.Mutex
	list []recordedEvent
}

type recordedEvent struct {
	name string
	data []any
}

func (l *eventLog) record(name string, data ...any) {
	l.mu.Lock()
	l.list = append(l.list, recordedEvent{name, data})
	l.mu.Unlock()
}

func (l *eventLog) count(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.list {
		if e.name == name {
			n++
		}
	}
	return n
}

type fakeNotifier struct {
	mu   sync.Mutex
	list []notify.Notification
}

func (f *fakeNotifier) Notify(_ context.Context, n notify.Notification) error {
	f.mu.Lock()
	f.list = append(f.list, n)
	f.mu.Unlock()
	return nil
}
func (f *fakeNotifier) OnActivated(func(string)) {}
func (f *fakeNotifier) Close() error             { return nil }

func (f *fakeNotifier) all() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list)
}

func testLogger() *slog.Logger {
	if os.Getenv("CZLMAIL_TEST_VERBOSE") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestApp 在临时配置目录里起一个还没登录过的 App。
func newTestApp(t *testing.T) *testApp {
	t.Helper()
	isolateConfig(t)
	resetOwnCache()
	return startTestApp(t, filepath.Join(t.TempDir(), "cache.db"))
}

// isolateConfig 给测试一个独立的数据目录 (config.json、笔记库)。
// macOS 上不能改 HOME: 钥匙串命令 security 按 HOME 找登录钥匙串, 找不到就卡住等输入;
// 那里改用独立的实例名 (数据目录 ~/Library/Application Support/czlmail-<实例>), 测试结束删掉。
func isolateConfig(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		signInTestConfig(t)
		return
	}
	t.Setenv("CZLMAIL_INSTANCE", fmt.Sprintf("go-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
}

// startTestApp 按当前配置目录与缓存库起 App, 与 startup 一样读配置、开库、后台恢复已保存的登录。
func startTestApp(t *testing.T, db string) *testApp {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ta := &testApp{t: t, ctx: ctx, cancel: cancel, db: db, events: &eventLog{}, notes: &fakeNotifier{}}
	a := &App{ctx: ctx, log: testLogger(), ready: make(chan struct{}), tap: ta.events.record}
	ta.a = a
	_, signIns := a.loadSignIns()
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.store = st
	a.notifier = ta.notes
	a.mu.Unlock()
	a.markReady()
	a.restoreSignIns(signIns)
	t.Cleanup(ta.stop)
	return ta
}

// stop 像退出程序一样停掉全部同步并关库。可以重复调用。
func (ta *testApp) stop() {
	ta.cancel()
	a := ta.a
	a.mu.Lock()
	for _, si := range a.signInsLocked() {
		if si.sync != nil {
			si.sync.Close()
		}
		si.stopLocked()
	}
	st := a.store
	a.store = nil
	a.mu.Unlock()
	a.notes.Lock()
	for key, service := range a.notes.services {
		_ = service.Close()
		delete(a.notes.services, key)
	}
	a.notes.Unlock()
	if st != nil {
		// 等后台协程 (预取、提醒等) 看到 context 取消后退出, 再关库; 关库后它们的写入只会报错。
		time.Sleep(100 * time.Millisecond)
		_ = st.Close()
	}
}

// restart 关掉这个实例, 用同一份配置与缓存库起一个新的。
func (ta *testApp) restart() *testApp {
	ta.t.Helper()
	ta.stop()
	resetOwnCache()
	return startTestApp(ta.t, ta.db)
}

func (ta *testApp) store() *store.Store {
	ta.t.Helper()
	st, err := ta.a.currentStore()
	if err != nil {
		ta.t.Fatal(err)
	}
	return st
}

func resetOwnCache() { resetOwnAddresses() }

// forgetPassword 在测试结束时删掉登录写进钥匙串的密码 (登录时按输入的用户名存)。
func forgetPassword(t *testing.T, srv *jmaptest.Server, username string) {
	endpoint := srv.SessionURL()
	t.Cleanup(func() { _ = config.DeletePassword(endpoint, username) })
}

func (ta *testApp) signInPrimary(srv *jmaptest.Server, username, password string) {
	ta.t.Helper()
	forgetPassword(ta.t, srv, username)
	if err := ta.a.SignInWithPassword(srv.URL(), username, password); err != nil {
		ta.t.Fatalf("sign in %s: %v", username, err)
	}
}

func (ta *testApp) addSignIn(srv *jmaptest.Server, username, password string) string {
	ta.t.Helper()
	forgetPassword(ta.t, srv, username)
	id, err := ta.a.AddSignInWithPassword(srv.URL(), username, password)
	if err != nil {
		ta.t.Fatalf("add sign-in %s: %v", username, err)
	}
	return id
}

// waitFor 每 20ms 检查一次条件, 10 秒内不成立就失败。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// subjects 返回缓存里某账号某邮箱的邮件主题 (按收件时间倒序)。
func (ta *testApp) subjects(accountID, mailboxID string) []string {
	ta.t.Helper()
	list, err := ta.store().EmailsByMailbox(ta.ctx, accountID, mailboxID, 200, 0)
	if err != nil {
		ta.t.Fatal(err)
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.Subject)
	}
	return out
}

func (ta *testApp) waitSubjects(accountID, mailboxID string, want ...string) {
	ta.t.Helper()
	waitFor(ta.t, accountID+"/"+mailboxID+" = "+join(want), func() bool {
		got := ta.subjects(accountID, mailboxID)
		slices.Sort(got)
		w := slices.Clone(want)
		slices.Sort(w)
		return slices.Equal(got, w)
	})
}

func join(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// cachedAccounts 返回缓存里的账号 id, 排序。
func (ta *testApp) cachedAccounts() []string {
	ta.t.Helper()
	accs, err := ta.store().Accounts(ta.ctx)
	if err != nil {
		ta.t.Fatal(err)
	}
	var out []string
	for _, acc := range accs {
		out = append(out, acc.ID)
	}
	slices.Sort(out)
	return out
}

func (ta *testApp) signInInfo(id string) SignInInfo {
	ta.t.Helper()
	list, err := ta.a.ListSignIns()
	if err != nil {
		ta.t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	ta.t.Fatalf("sign-in %q not listed: %+v", id, list)
	return SignInInfo{}
}

// connected 报告某个登录的会话是否已建立。
func (ta *testApp) connected(id string) bool {
	ta.a.mu.RLock()
	defer ta.a.mu.RUnlock()
	si := ta.a.signInLocked(id)
	return si != nil && si.sync != nil
}

// emailCount 数缓存里某账号的邮件。
func (ta *testApp) emailCount(accountID string) int {
	ta.t.Helper()
	var n int
	if err := ta.store().DB().QueryRowContext(ta.ctx, `SELECT count(*) FROM emails WHERE account_id = ?`, accountID).Scan(&n); err != nil {
		ta.t.Fatal(err)
	}
	return n
}

// countRows 数缓存库里账号 id 以 prefix~ 开头的行 (全部按账号存的表), 用来查孤儿数据。
func (ta *testApp) countRows(prefix string) int {
	ta.t.Helper()
	p := prefix + accountid.Separator
	n := 0
	for _, table := range []string{"emails", "mailboxes", "email_mailboxes", "email_keywords", "threads", "sync_state", "translations", "trusted_senders"} {
		var c int
		if err := ta.store().DB().QueryRowContext(ta.ctx, `SELECT count(*) FROM `+table+` WHERE substr(account_id, 1, ?) = ?`, len(p), p).Scan(&c); err != nil {
			ta.t.Fatalf("count %s: %v", table, err)
		}
		n += c
	}
	var c int
	if err := ta.store().DB().QueryRowContext(ta.ctx, `SELECT count(*) FROM accounts WHERE substr(id, 1, ?) = ?`, len(p), p).Scan(&c); err != nil {
		ta.t.Fatal(err)
	}
	return n + c
}
