package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/upstream"
)

// czlmail 网关的客户端支持 (#46): 一个网关登录下每个上游邮箱是一个个人账号, 账号能力带上游状态,
// 上游不可用时方法返回 serverUnavailable + czlUpstream。用 jmaptest 模拟网关, App 走真实的登录与同步路径。

// gatewayServer 是一个网关用户 wood, 下有 Gmail 与 Outlook 两个上游邮箱, 默认邮箱是 Outlook。
// 默认邮箱故意不是按名称排在前面的那个: personalAccountID 要按 primaryAccounts 取, 不按遍历顺序。
type gatewayServer struct {
	srv           *jmaptest.Server
	user          *jmaptest.User
	gmail, outlok *jmaptest.Account
}

func newGatewayServer(t *testing.T) gatewayServer {
	t.Helper()
	srv := jmaptest.New(t)
	u := srv.AddUser("wood", "app-pw")
	g := srv.AddAccount("g", "wood@gmail.test", jmaptest.Mail, jmaptest.Submission)
	m := srv.AddAccount("m", "wood@outlook.test", jmaptest.Mail, jmaptest.Submission)
	g.SetCapability(jmaptest.Upstream, upstreamOK("gmail"))
	m.SetCapability(jmaptest.Upstream, upstreamOK("microsoft"))
	u.Grant(g, true).Grant(m, true)
	u.SetPrimary(m)
	return gatewayServer{srv: srv, user: u, gmail: g, outlok: m}
}

func upstreamOK(kind string) map[string]any {
	return map[string]any{"kind": kind, "status": "ok", "statusSince": "2026-11-20T08:00:00Z",
		"code": nil, "reauthUrl": nil, "initialSync": nil}
}

// accountByID 从 ListAccounts 里取一个账号。
func (ta *testApp) accountByID(id string) store.Account {
	ta.t.Helper()
	list, err := ta.a.ListAccounts()
	if err != nil {
		ta.t.Fatal(err)
	}
	for _, acc := range list {
		if acc.ID == id {
			return acc
		}
	}
	ta.t.Fatalf("account %q not listed", id)
	return store.Account{}
}

// TestGatewayTwoPersonalAccounts 一个登录下两个个人账号: 都同步、写信能在两者间切换、默认邮箱取 primaryAccounts;
// 其中一个变成 authRequired 后另一个照常同步, 这一轮不算失败、不要求重新登录, 账号上显示需要重新授权。
func TestGatewayTwoPersonalAccounts(t *testing.T) {
	gw := newGatewayServer(t)
	gw.gmail.AddEmail(jmaptest.Email{Subject: "from gmail", From: "x@ext.test"})
	gw.outlok.AddEmail(jmaptest.Email{Subject: "from outlook", From: "y@ext.test"})
	ta := newTestApp(t)
	ta.signInPrimary(gw.srv, "wood", "app-pw")
	a := ta.a
	ta.waitSubjects("g", "inbox", "from gmail")
	ta.waitSubjects("m", "inbox", "from outlook")

	if got := a.personalAccountID(ta.store()); got != "m" {
		t.Fatalf("personalAccountID = %q, want the primary account m", got)
	}
	if acc := ta.accountByID("g"); acc.Upstream == nil || acc.Upstream.Kind != "gmail" || !acc.Upstream.OK() || acc.Primary {
		t.Fatalf("gmail account = %+v", acc)
	}

	// 发件人下拉列出同一登录里的两个个人账号; 从 Gmail 账号写信时它排第一。
	groups, err := a.ListComposeIdentities("g")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].AccountID != "g" || groups[1].AccountID != "m" ||
		len(groups[1].Identities) != 1 || groups[1].Identities[0].Email != "wood@outlook.test" {
		t.Fatalf("compose identity groups = %+v", groups)
	}
	// 换成 Outlook 的身份发出: 落在账号 m。
	if err := a.SendEmail(ComposeRequest{
		AccountID: "m", IdentityID: groups[1].Identities[0].ID, To: []store.Address{{Email: "z@ext.test"}},
		Subject: "via outlook", TextBody: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	if subs := gw.srv.Submissions(); len(subs) != 1 || subs[0].AccountID != "m" || !slices.Contains(subs[0].From, "wood@outlook.test") {
		t.Fatalf("submissions = %+v", subs)
	}

	// Outlook 需要重新授权: 网关改账号能力 (会话 state 随之变) 并断开推送。
	reauth := "https://gw.test/admin/upstreams/m/reauth"
	gw.outlok.SetUnavailable(upstream.StatusAuthRequired, 40110)
	gw.outlok.SetCapability(jmaptest.Upstream, map[string]any{"kind": "microsoft", "status": "authRequired",
		"statusSince": "2026-11-26T09:00:00Z", "code": 40110, "reauthUrl": reauth, "initialSync": nil})
	gw.srv.DropStreams()
	waitFor(t, "outlook marked authRequired", func() bool {
		u := ta.accountByID("m").Upstream
		return u != nil && u.Status == upstream.StatusAuthRequired && u.ReauthURL == reauth && u.Code == 40110
	})
	waitFor(t, "reconnect round finished", func() bool {
		st := a.GetMailSyncStatus()
		return !st.Running && st.Total == 2
	})

	// Gmail 照常同步。
	gw.gmail.AddEmail(jmaptest.Email{Subject: "still flowing", From: "x@ext.test"})
	ta.waitSubjects("g", "inbox", "from gmail", "still flowing")

	st := a.GetMailSyncStatus()
	if st.Error != "" || slices.Contains(st.Pending, "m") {
		t.Fatalf("sync status = %+v, want no round error and m not pending", st)
	}
	if ta.events.count(EventSignInRequired) != 0 || !ta.connected("") {
		t.Fatal("an unavailable upstream must not end the gateway sign-in")
	}
	// 缓存里 Outlook 的邮件还在, 之前的数据不因上游不可用丢掉。
	if got := ta.subjects("m", "inbox"); !slices.Contains(got, "from outlook") {
		t.Fatalf("outlook cache = %v", got)
	}

	// 对这个账号的操作报网关的上游码, 界面据此显示「需要重新授权」。
	if _, err := a.ListIdentities("m"); err == nil || !strings.Contains(err.Error(), "40110") {
		t.Fatalf("identities of an authRequired upstream: %v", err)
	}
	// 发件人下拉里取不到它的身份就不列, Gmail 照样能选。
	groups, err = a.ListComposeIdentities("g")
	if err != nil || len(groups) != 1 || groups[0].AccountID != "g" {
		t.Fatalf("compose groups while outlook is unavailable = %+v, %v", groups, err)
	}

	// 重新授权后恢复: 账号再同步一次, 期间到的信也取回来。
	gw.outlok.AddEmail(jmaptest.Email{Subject: "after reauth", From: "y@ext.test"})
	gw.outlok.SetUnavailable("", 0)
	gw.outlok.SetCapability(jmaptest.Upstream, upstreamOK("microsoft"))
	gw.srv.DropStreams()
	ta.waitSubjects("m", "inbox", "from outlook", "after reauth")
	if !ta.accountByID("m").Upstream.OK() {
		t.Fatal("outlook still marked unavailable")
	}
}

// TestGatewaySessionStateChangeRereadsSession API 响应里的 sessionState 变了就重读会话, 不必等推送断开。
func TestGatewaySessionStateChangeRereadsSession(t *testing.T) {
	gw := newGatewayServer(t)
	ta := newTestApp(t)
	ta.signInPrimary(gw.srv, "wood", "app-pw")
	ta.waitSubjects("g", "inbox")

	// 网关开始首次导入 Outlook: 只改了会话 (state 跟着变), 推送没有断。
	gw.outlok.SetCapability(jmaptest.Upstream, map[string]any{"kind": "microsoft", "status": "syncing",
		"statusSince": "2026-11-26T09:00:00Z", "code": 50310, "reauthUrl": nil, "initialSync": map[string]any{"done": 1200, "total": 4800}})
	// 任意一次请求带回新的 sessionState: 新信触发推送, 客户端拉取时看到。
	gw.gmail.AddEmail(jmaptest.Email{Subject: "ping", From: "x@ext.test"})
	ta.waitSubjects("g", "inbox", "ping")
	waitFor(t, "session re-read after sessionState changed", func() bool {
		u := ta.accountByID("m").Upstream
		return u != nil && u.Status == upstream.StatusSyncing && u.InitialSync != nil && u.InitialSync.Done == 1200
	})
	if n := gw.srv.StreamCount(gw.user); n != 1 {
		t.Fatalf("push streams = %d, the re-read should not need a reconnect", n)
	}
}

// TestGatewayNoUpstream 网关用户还没有任何邮箱时会话回 403 problem JSON, 登录报 2066 而不是凭据错。
func TestGatewayNoUpstream(t *testing.T) {
	srv := jmaptest.New(t)
	u := srv.AddUser("wood", "app-pw")
	u.SetNoUpstream(true)
	ta := newTestApp(t)
	forgetPassword(t, srv, "wood")
	err := ta.a.SignInWithPassword(srv.URL(), "wood", "app-pw")
	if err == nil || !strings.HasPrefix(err.Error(), "2066 ") {
		t.Fatalf("sign in without upstream: %v", err)
	}
}

// TestComposeIdentityAccountsSameSignIn 同一登录里所有能发信的个人账号都列, 共享账号与不能发信的个人账号不列。
func TestComposeIdentityAccountsSameSignIn(t *testing.T) {
	send := store.Account{IsPersonal: true}
	send.Features.Submission = true
	acc := func(id string, base store.Account) store.Account { base.ID = id; return base }
	accounts := []store.Account{
		acc("g", send), acc("m", send), acc("ro", store.Account{IsPersonal: true}),
		acc("team", store.Account{}), acc("s2~a", send),
	}
	var got []string
	for _, a := range composeIdentityAccounts(accounts, "team") {
		got = append(got, a.ID)
	}
	if want := []string{"team", "g", "m", "s2~a"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = nil
	for _, a := range composeIdentityAccounts(accounts, "m") {
		got = append(got, a.ID)
	}
	if want := []string{"m", "g", "s2~a"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseSignInHintGateway(t *testing.T) {
	h, err := parseSignInHint(strings.NewReader(`{"version":1,"gateway":{"name":"czlmail 网关","adminUrl":"https://mail.example.net/admin/","protocolVersion":1,"version":"2026.11.0"}}`), "src")
	if err != nil || h.Gateway == nil || h.Gateway.AdminURL != "https://mail.example.net/admin/" || h.ClientID != "" {
		t.Fatalf("gateway hint = %+v, %v", h, err)
	}
	h, err = parseSignInHint(strings.NewReader(`{"version":1,"gateway":{"name":"x","adminUrl":"javascript:alert(1)"}}`), "src")
	if err != nil || h.Gateway == nil || h.Gateway.AdminURL != "" {
		t.Fatalf("unsafe admin url kept: %+v, %v", h, err)
	}
	h, err = parseSignInHint(strings.NewReader(`{"version":1,"oauth":{"name":"CZL","issuer":"https://connect.example.net","clientId":"c"}}`), "src")
	if err != nil || h.Gateway != nil || h.ClientID != "c" {
		t.Fatalf("oauth hint = %+v, %v", h, err)
	}
	if _, err := parseSignInHint(strings.NewReader(`{"version":1}`), "src"); err == nil {
		t.Fatal("empty config accepted")
	}
}
