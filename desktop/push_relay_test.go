package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// fakePushSession 是一个登录的会话: 只认自己前缀的账号, 记下推送订阅的增改删。
type fakePushSession struct {
	prefix  string
	mu      sync.Mutex
	subs    map[string]syncer.PushSubscription
	next    int
	created []map[string]any
	updated []map[string]map[string]any
	deleted []string
}

func newFakePushSession(prefix string) *fakePushSession {
	return &fakePushSession{prefix: prefix, subs: map[string]syncer.PushSubscription{}}
}

func (f *fakePushSession) ServerID(local string) string {
	_, id := accountid.Split(local)
	return id
}
func (f *fakePushSession) Owns(local string) bool {
	p, _ := accountid.Split(local)
	return p == f.prefix
}
func (f *fakePushSession) SupportsEmailPush(string) bool { return true }
func (f *fakePushSession) PushSubscriptions(context.Context) ([]syncer.PushSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]syncer.PushSubscription, 0, len(f.subs))
	for _, s := range f.subs {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakePushSession) SetPushSubscription(_ context.Context, create map[string]any, update map[string]map[string]any, destroy []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := ""
	if create != nil {
		f.next++
		id = fmt.Sprintf("%s-sub%d", f.prefix, f.next)
		f.subs[id] = syncer.PushSubscription{ID: id, DeviceClientID: create["deviceClientId"].(string), URL: create["url"].(string)}
		f.created = append(f.created, create)
	}
	if update != nil {
		f.updated = append(f.updated, update)
	}
	for _, d := range destroy {
		delete(f.subs, d)
		f.deleted = append(f.deleted, d)
	}
	return id, nil
}

// fakeRelay 是推送中转: 每次 POST /v1/subscriptions 发一个新登记, pending 里放该登记对应订阅的验证码。
type fakeRelay struct {
	mu      sync.Mutex
	regs    map[string]bool
	n       int
	calls   []string
	pending map[string]string // 登记 id → JMAP 订阅 id
}

func (r *fakeRelay) call(_ context.Context, method, path, secret string, in, out any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, method+" "+path)
	switch {
	case method == http.MethodPost && path == "/v1/subscriptions":
		r.n++
		id := fmt.Sprintf("relay%d", r.n)
		r.regs[id] = true
		raw, _ := json.Marshal(map[string]string{"id": id, "secret": "secret-" + id, "url": "https://relay.example/" + id})
		return json.Unmarshal(raw, out)
	case strings.HasSuffix(path, "/pending"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/subscriptions/"), "/pending")
		sub := r.pending[id]
		msg, _ := json.Marshal(map[string]string{"@type": "PushVerification", "pushSubscriptionId": sub, "verificationCode": "code-" + sub})
		raw, _ := json.Marshal(map[string][]string{"messages": {string(msg)}})
		return json.Unmarshal(raw, out)
	case method == http.MethodDelete:
		id := strings.TrimPrefix(path, "/v1/subscriptions/")
		if !r.regs[id] {
			return errRelayNotFound
		}
		delete(r.regs, id)
	case method == http.MethodPut:
		id := strings.TrimPrefix(path, "/v1/subscriptions/")
		if !r.regs[id] {
			return errRelayNotFound
		}
	}
	return nil
}

// pushTestApp 组装一个有主登录 (账号 c) 与附加登录 s2 (账号 s2~c, 与主登录的账号 id 相同) 的 App, 两边都有收件箱。
func pushTestApp(t *testing.T) (*App, *store.Store, *fakeRelay) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertAccounts(ctx, tx, []store.Account{{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}}, nil); err != nil {
			return err
		}
		for _, acc := range []string{"c", "s2~c"} {
			if err := store.UpsertMailboxes(ctx, tx, acc, []store.Mailbox{{ID: "inbox", Name: "Inbox", Role: "inbox"}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil)), store: st}
	a.primary.cfg = config.Config{Username: "me@one.example"}
	a.extra = []*signIn{{id: "s2", cfg: config.Config{Username: "me@two.example"}}}
	forgetOwnAddresses()
	t.Cleanup(forgetOwnAddresses)

	relay := &fakeRelay{regs: map[string]bool{}, pending: map[string]string{}}
	old, oldEnabled := pushDeps, pushEnabled
	pushEnabled = true
	pushDeps = pushHooks{
		deviceInfo: func(context.Context) (pushInfo, error) {
			return pushInfo{Token: "tok", P256DH: "key1", Auth: "auth", DeviceID: "dev1"}, nil
		},
		decrypt:    func(_ context.Context, body string) (string, error) { return body, nil },
		relay:      relay.call,
		verifyPoll: time.Millisecond,
	}
	t.Cleanup(func() {
		pushDeps, pushEnabled = old, oldEnabled
		setPushCovered(accountid.Primary, nil)
		setPushCovered("s2", nil)
	})
	return a, st, relay
}

// syncPushVerified 跑一轮 syncPush; 新建订阅时让中转回该订阅的验证码。
func syncPushVerified(t *testing.T, a *App, relay *fakeRelay, signIn string, s *fakePushSession, st *store.Store) {
	t.Helper()
	// 订阅 id 在创建时才知道: 预先按假会话的编号规则登记好验证消息。
	relay.mu.Lock()
	next := fmt.Sprintf("%s-sub%d", s.prefix, s.next+1)
	for id := range relay.regs {
		relay.pending[id] = next
	}
	relay.pending[fmt.Sprintf("relay%d", relay.n+1)] = next
	relay.mu.Unlock()
	if err := a.syncPush(context.Background(), signIn, s, st); err != nil {
		t.Fatalf("syncPush(%q): %v", signIn, err)
	}
}

// 两个登录各自建一份订阅与中转登记, 只覆盖自己的账号; 推送通知按中转登记认回正确的本机账号。
func TestPushPerSignIn(t *testing.T) {
	a, st, relay := pushTestApp(t)
	ctx := context.Background()
	primary, second := newFakePushSession(accountid.Primary), newFakePushSession("s2")

	syncPushVerified(t, a, relay, accountid.Primary, primary, st)
	syncPushVerified(t, a, relay, "s2", second, st)

	if len(primary.created) != 1 || len(second.created) != 1 {
		t.Fatalf("每个登录应各建一个订阅, 实际 %d / %d", len(primary.created), len(second.created))
	}
	// 订阅里的账号是服务器上的 id, 只含自己的账号。
	for name, s := range map[string]*fakePushSession{"primary": primary, "s2": second} {
		ep := s.created[0]["emailPush"].(map[string]any)
		if len(ep) != 1 || ep["c"] == nil {
			t.Errorf("%s 的订阅应只推服务器账号 c, 实际 %v", name, ep)
		}
	}
	regs := loadPushRegs(ctx, st)
	if len(regs) != 2 || regs[""].ID == regs["s2"].ID || regs[""].ID == "" || regs["s2"].ID == "" {
		t.Fatalf("两个登录应各有一份中转登记, 实际 %+v", regs)
	}
	if regs[""].SubID != "-sub1" || regs["s2"].SubID != "s2-sub1" {
		t.Errorf("登记里的订阅 id 不对: %+v", regs)
	}
	if !a.pushCovers("c") || !a.pushCovers("s2~c") {
		t.Error("两个登录的账号都应已由推送覆盖")
	}
	// 自己 (任一登录) 发的信不推。
	filter, _ := json.Marshal(second.created[0]["emailPush"])
	for _, addr := range []string{"me@one.example", "me@two.example"} {
		if !strings.Contains(string(filter), addr) {
			t.Errorf("推送过滤应排除自己的地址 %s: %s", addr, filter)
		}
	}

	// 点开推送: 中转登记 + 服务器账号 id → 本机账号 id。
	acc, id, ok := a.mailActionTarget("push:" + regs["s2"].ID + "/c/m1")
	if !ok || acc != "s2~c" || id != "m1" {
		t.Errorf("s2 的推送应打开 s2~c/m1, 实际 %q %q %v", acc, id, ok)
	}
	if acc, _, _ := a.mailActionTarget("push:" + regs[""].ID + "/c/m1"); acc != "c" {
		t.Errorf("主登录的推送应打开 c, 实际 %q", acc)
	}
	if _, _, ok := a.mailActionTarget("push:unknown/c/m1"); ok {
		t.Error("认不出的中转登记不应打开任何邮件")
	}

	// 续期: 订阅还在, 只更新过期时间与账号, 不新建。
	if err := a.syncPush(ctx, "s2", second, st); err != nil {
		t.Fatal(err)
	}
	if len(second.created) != 1 || len(second.updated) != 2 { // 第一次更新是写验证码
		t.Errorf("续期应只更新订阅, 实际新建 %d 更新 %d", len(second.created), len(second.updated))
	}
	if last := second.updated[len(second.updated)-1]["s2-sub1"]; last["expires"] == nil {
		t.Errorf("续期应带新的过期时间: %v", last)
	}
	if len(primary.created) != 1 {
		t.Error("续期 s2 不应碰主登录的订阅")
	}
}

// 静音一个登录的全部账号: 只撤它的订阅与中转登记, 另一个登录照旧。
func TestPushDropsOnlyMutedSignIn(t *testing.T) {
	a, st, relay := pushTestApp(t)
	ctx := context.Background()
	primary, second := newFakePushSession(accountid.Primary), newFakePushSession("s2")
	syncPushVerified(t, a, relay, accountid.Primary, primary, st)
	syncPushVerified(t, a, relay, "s2", second, st)

	if err := st.SetStringSetting(ctx, "mutedMailAccounts", `["s2~c"]`); err != nil {
		t.Fatal(err)
	}
	if err := a.syncPush(ctx, "s2", second, st); err != nil {
		t.Fatal(err)
	}
	if len(second.subs) != 0 || len(second.deleted) != 1 {
		t.Errorf("s2 的订阅应被删掉, 剩 %v", second.subs)
	}
	regs := loadPushRegs(ctx, st)
	if _, ok := regs["s2"]; ok || regs[""].ID == "" {
		t.Errorf("只应删掉 s2 的登记, 实际 %+v", regs)
	}
	if a.pushCovers("s2~c") || !a.pushCovers("c") {
		t.Error("s2 不再由推送覆盖, 主登录照旧")
	}
}

// 退出 / 移除一个登录: 会话在时删服务器订阅与中转登记; 会话不在时只删中转登记; 别的登录不动。
func TestStopPushPerSignIn(t *testing.T) {
	a, st, relay := pushTestApp(t)
	ctx := context.Background()
	primary, second := newFakePushSession(accountid.Primary), newFakePushSession("s2")
	syncPushVerified(t, a, relay, accountid.Primary, primary, st)
	syncPushVerified(t, a, relay, "s2", second, st)
	regs := loadPushRegs(ctx, st)

	// s2 的会话已经没了 (stopPush 读 signIn.sync, 测试里没有真同步器): 只删中转登记。
	a.stopPush("s2")
	if relay.regs[regs["s2"].ID] {
		t.Error("s2 的中转登记应已删除")
	}
	if !relay.regs[regs[""].ID] {
		t.Error("主登录的中转登记不应受影响")
	}
	if got := loadPushRegs(ctx, st); len(got) != 1 || got[""].ID == "" {
		t.Errorf("本机只应剩主登录的登记, 实际 %+v", got)
	}
	if a.pushCovers("s2~c") || !a.pushCovers("c") {
		t.Error("s2 不再覆盖, 主登录照旧")
	}

	// 主登录换人: forgetPush 只删主登录的登记。
	syncPushVerified(t, a, relay, "s2", second, st)
	a.forgetPush(ctx, st)
	got := loadPushRegs(ctx, st)
	if _, ok := got[""]; ok || got["s2"].ID == "" {
		t.Errorf("换人只应删主登录的登记, 实际 %+v", got)
	}
}

// 旧版只存了一份登记 (属于主登录): 升级后认作主登录的登记, 写回时换成按登录分开的格式。
func TestPushLegacyRegistration(t *testing.T) {
	a, st, _ := pushTestApp(t)
	ctx := context.Background()
	legacy := `{"id":"old","secret":"s","url":"https://relay.example/old","keyId":"key1","subId":"sub-old","accounts":["c"]}`
	if err := st.SetStringSetting(ctx, settingPushRelay, legacy); err != nil {
		t.Fatal(err)
	}
	if reg := loadPushReg(ctx, st, accountid.Primary); reg.ID != "old" || reg.SubID != "sub-old" {
		t.Fatalf("旧登记应属于主登录, 实际 %+v", reg)
	}
	if reg := loadPushReg(ctx, st, "s2"); reg.ID != "" {
		t.Errorf("附加登录不应继承旧登记: %+v", reg)
	}
	a.primePush(ctx, accountid.Primary, st)
	if !a.pushCovers("c") || a.pushCovers("s2~c") {
		t.Error("启动时应按旧登记认定主登录的账号已覆盖")
	}
	savePushReg(ctx, st, "s2", pushRelayReg{ID: "new"})
	raw, _ := st.StringSetting(ctx, settingPushRelay)
	var state pushRelayState
	if err := json.Unmarshal([]byte(raw), &state); err != nil || state.SignIns[""].ID != "old" || state.SignIns["s2"].ID != "new" {
		t.Errorf("写回后应是按登录分开的格式, 实际 %s", raw)
	}
}

// 唤醒要让每个登录的推送循环都醒: 用关闭通道广播。
func TestWakePushReachesEveryLoop(t *testing.T) {
	a := &App{}
	w1, w2 := pushWake(), pushWake()
	a.wakePush()
	for i, w := range []<-chan struct{}{w1, w2} {
		select {
		case <-w:
		case <-time.After(time.Second):
			t.Fatalf("循环 %d 没被唤醒", i)
		}
	}
	select {
	case <-pushWake():
		t.Error("唤醒之后应换上新的通道")
	default:
	}
}

func TestSetPushCoveredPerSignIn(t *testing.T) {
	a := &App{}
	t.Cleanup(func() { setPushCovered("", nil); setPushCovered("s2", nil) })
	setPushCovered("", []string{"c"})
	setPushCovered("s2", []string{"s2~c"})
	setPushCovered("", nil)
	if a.pushCovers("c") || !a.pushCovers("s2~c") {
		t.Error("清掉主登录的覆盖不应影响 s2")
	}
	if !slices.Contains(push.covered["s2"], "s2~c") {
		t.Error(push.covered)
	}
}
