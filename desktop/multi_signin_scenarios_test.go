package main

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// twoServers 是场景 1 的布局: 两台服务器, 个人账号 id 都是 "a", 收件箱里的第一封信 id 也都是 "m1"。
type twoServers struct {
	one, two     *jmaptest.Server
	alice, carol *jmaptest.User
	oneA, twoA   *jmaptest.Account
}

func newTwoServers(t *testing.T) *twoServers {
	t.Helper()
	ts := &twoServers{one: jmaptest.New(t), two: jmaptest.New(t)}
	ts.alice = ts.one.AddUser("alice@one.test", "pw-alice")
	ts.oneA = ts.one.AddAccount("a", "alice@one.test")
	ts.alice.Grant(ts.oneA, true)
	ts.carol = ts.two.AddUser("carol@two.test", "pw-carol")
	ts.twoA = ts.two.AddAccount("a", "carol@two.test")
	ts.carol.Grant(ts.twoA, true)
	at := time.Now().Add(-time.Hour)
	ts.oneA.AddEmail(jmaptest.Email{ID: "m1", Subject: "hello from one", From: "x@ext.test", ReceivedAt: at})
	ts.twoA.AddEmail(jmaptest.Email{ID: "m1", Subject: "hello from two", From: "y@ext.test", ReceivedAt: at})
	return ts
}

// signIn 让 ta 以 alice 为主登录、carol 为附加登录, 等两边都引导完。返回附加登录的前缀。
func (ts *twoServers) signIn(ta *testApp) string {
	ta.t.Helper()
	ta.signInPrimary(ts.one, "alice@one.test", "pw-alice")
	s2 := ta.addSignIn(ts.two, "carol@two.test", "pw-carol")
	ta.waitSubjects("a", "inbox", "hello from one")
	ta.waitSubjects(s2+"~a", "inbox", "hello from two")
	return s2
}

// 场景 1: 两台服务器的账号 id 撞车。缓存各存各的、互不删除; 统一视图、搜索、未读数覆盖两边;
// 写操作发到正确的服务器, 用的是服务器账号 id 而不是本机 id。
func TestMultiSignInCollidingAccountIDs(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	if s2 != "s2" {
		t.Fatalf("first added sign-in got prefix %q, want s2", s2)
	}
	local2 := s2 + "~a"
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", local2}) {
		t.Fatalf("cached accounts = %v", got)
	}

	// 两个同步器各自重读会话、清理账号: 只看自己的前缀, 谁也不把对方的账号当成"会话里没有了"删掉。
	for range 3 {
		for _, acc := range []string{"a", local2} {
			s, err := ta.a.syncerFor(acc)
			if err != nil {
				t.Fatal(err)
			}
			if removed := s.PruneAccounts(ta.ctx); len(removed) != 0 {
				t.Fatalf("syncer of %s pruned %v", acc, removed)
			}
		}
	}
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", local2}) {
		t.Fatalf("cached accounts after pruning = %v", got)
	}

	// 统一视图、搜索、未读数。
	unified, err := ta.a.ListUnified(store.KindInbox, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unified) != 2 {
		t.Fatalf("unified inbox = %+v", unified)
	}
	found, err := ta.a.SearchAllEmails("hello", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("search across sign-ins = %+v", found)
	}
	if n := ta.a.unreadTotal(); n != 2 {
		t.Fatalf("unread total = %d, want 2", n)
	}

	// 对附加登录的邮件操作: 只到第二台服务器, 账号 id 是 "a"。
	ts.one.ResetCalls()
	ts.two.ResetCalls()
	if err := ta.a.MarkRead(local2, []string{"m1"}, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := ts.twoA.EmailByID("m1"); !slices.Contains(info.Keywords, "$seen") {
		t.Fatalf("server two keywords = %v", info.Keywords)
	}
	if info, _ := ts.oneA.EmailByID("m1"); slices.Contains(info.Keywords, "$seen") {
		t.Fatal("marking s2~a/m1 read also marked a/m1 on server one")
	}
	waitFor(t, "unread total 1", func() bool { return ta.a.unreadTotal() == 1 })

	if err := ta.a.MoveEmails(local2, []string{"m1"}, "archive"); err != nil {
		t.Fatal(err)
	}
	if info, _ := ts.twoA.EmailByID("m1"); !slices.Equal(info.Mailboxes, []string{"archive"}) {
		t.Fatalf("server two mailboxes after move = %v", info.Mailboxes)
	}
	ta.waitSubjects(local2, "archive", "hello from two")
	ta.waitSubjects("a", "inbox", "hello from one")

	ids, err := ta.a.ListIdentities(local2)
	if err != nil || len(ids) != 1 || ids[0].Email != "carol@two.test" {
		t.Fatalf("identities of %s = %+v, %v", local2, ids, err)
	}
	if err := ta.a.SendEmail(ComposeRequest{
		AccountID: local2, IdentityID: ids[0].ID,
		To: []store.Address{{Email: "z@ext.test"}}, Subject: "from carol", TextBody: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	if subs := ts.two.Submissions(); len(subs) != 1 || subs[0].AccountID != "a" || subs[0].User != "carol@two.test" {
		t.Fatalf("server two submissions = %+v", subs)
	}
	if subs := ts.one.Submissions(); len(subs) != 0 {
		t.Fatalf("server one got a submission for s2: %+v", subs)
	}
	ta.waitSubjects(local2, "sent", "from carol")

	if err := ta.a.DeleteEmails(local2, []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.twoA.EmailByID("m1"); ok {
		t.Fatal("delete did not reach server two")
	}
	if _, ok := ts.oneA.EmailByID("m1"); !ok {
		t.Fatal("deleting s2~a/m1 deleted a/m1 on server one")
	}
	ta.waitSubjects(local2, "archive")
	ta.waitSubjects("a", "inbox", "hello from one")

	for _, c := range ts.one.Calls() {
		if c.Method != "Mailbox/get" && c.Method != "Mailbox/changes" && c.Method != "Email/changes" && c.Method != "Email/get" && c.Method != "Identity/get" {
			t.Errorf("server one got %s while operating on %s", c.Method, local2)
		}
	}
	for _, c := range ts.two.Calls() {
		if c.AccountID != "" && c.AccountID != "a" {
			t.Errorf("server two got %s for account %q, want the server id \"a\"", c.Method, c.AccountID)
		}
	}

	// 主登录的操作同理只到第一台。
	ts.two.ResetCalls()
	if err := ta.a.MarkRead("a", []string{"m1"}, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := ts.oneA.EmailByID("m1"); !slices.Contains(info.Keywords, "$seen") {
		t.Fatal("primary MarkRead did not reach server one")
	}
	for _, c := range ts.two.CallsTo("Email/set") {
		t.Errorf("server two got Email/set %s for a primary operation", c.Args)
	}

	// 推送来的新信落到对应的本机账号。
	ts.twoA.AddEmail(jmaptest.Email{Subject: "pushed to two", From: "y@ext.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "pushed to one", From: "x@ext.test"})
	ta.waitSubjects(local2, "inbox", "pushed to two")
	ta.waitSubjects("a", "inbox", "hello from one", "pushed to one")
}

// 场景 2: 同一台服务器上的两个用户都能看到同一个共享账号。共享账号在两个登录下各有一份缓存, 互不干扰。
func TestMultiSignInSameServerSharedAccount(t *testing.T) {
	srv := jmaptest.New(t)
	alice := srv.AddUser("alice@one.test", "pw-alice")
	bob := srv.AddUser("bob@one.test", "pw-bob")
	a := srv.AddAccount("a", "alice@one.test")
	b := srv.AddAccount("b", "bob@one.test")
	team := srv.AddAccount("t", "team@one.test")
	alice.Grant(a, true).Grant(team, false)
	bob.Grant(b, true).Grant(team, false)
	team.AddEmail(jmaptest.Email{ID: "m1", Subject: "team mail", From: "x@ext.test"})
	b.AddEmail(jmaptest.Email{Subject: "bob's own", From: "x@ext.test"})

	ta := newTestApp(t)
	ta.signInPrimary(srv, "alice@one.test", "pw-alice")
	s2 := ta.addSignIn(srv, "bob@one.test", "pw-bob")
	ta.waitSubjects("t", "inbox", "team mail")
	ta.waitSubjects(s2+"~t", "inbox", "team mail")
	ta.waitSubjects(s2+"~b", "inbox", "bob's own")
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", s2 + "~b", s2 + "~t", "t"}) {
		t.Fatalf("cached accounts = %v", got)
	}

	// 同一个人不能登录两次。
	if _, err := ta.a.AddSignInWithPassword(srv.URL(), "BOB@one.test", "pw-bob"); err == nil {
		t.Fatal("bob signed in twice")
	}

	// 用 bob 的登录把共享邮件标已读: 服务器上只有一封, 两份缓存最终都看到已读 (都从服务器同步来)。
	srv.ResetCalls()
	if err := ta.a.MarkRead(s2+"~t", []string{"m1"}, true); err != nil {
		t.Fatal(err)
	}
	sets := srv.CallsTo("Email/set")
	if len(sets) != 1 || sets[0].User != "bob@one.test" || sets[0].AccountID != "t" {
		t.Fatalf("Email/set calls = %+v", sets)
	}
	waitFor(t, "both copies read", func() bool {
		x, _ := ta.store().Email(ta.ctx, "t", "m1")
		y, _ := ta.store().Email(ta.ctx, s2+"~t", "m1")
		return x != nil && y != nil && !x.IsUnread && !y.IsUnread
	})

	// 撤销 bob 对共享账号的访问: 连续两次缺失后只删 bob 那份, alice 那份不动。
	bob.Revoke(team)
	s, err := ta.a.syncerFor(s2 + "~b")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s.RequestSessionRefresh()
		time.Sleep(300 * time.Millisecond)
	}
	waitFor(t, "bob's copy of the shared account removed", func() bool {
		return slices.Equal(ta.cachedAccounts(), []string{"a", s2 + "~b", "t"})
	})
	ta.waitSubjects("t", "inbox", "team mail")
}

// 场景 6: 会话里的共享账号在运行中增减。新共享的账号按前缀引导; 少了的账号要连续两次缺失才删, 只删自己前缀下的。
func TestMultiSignInSessionRefreshScopedByPrefix(t *testing.T) {
	ts := newTwoServers(t)
	shared1 := ts.one.AddAccount("x", "shared@one.test")
	shared2 := ts.two.AddAccount("x", "shared@two.test") // 两边的共享账号 id 也撞车
	shared1.AddEmail(jmaptest.Email{Subject: "shared on one"})
	shared2.AddEmail(jmaptest.Email{Subject: "shared on two"})
	ts.alice.Grant(shared1, false)
	ts.carol.Grant(shared2, false)

	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	ta.waitSubjects("x", "inbox", "shared on one")
	ta.waitSubjects(s2+"~x", "inbox", "shared on two")
	if err := ta.store().SaveTranslation(ta.ctx, s2+"~x", "e1", "en", "local only"); err != nil {
		t.Fatal(err)
	}
	if err := ta.store().SaveTranslation(ta.ctx, "x", "e1", "en", "local only"); err != nil {
		t.Fatal(err)
	}

	// 主登录失去 x: 第一次缺失不删, 第二次删, 只删 "x", 不碰 "s2~x"。
	ts.alice.Revoke(shared1)
	primary, err := ta.a.syncerFor("a")
	if err != nil {
		t.Fatal(err)
	}
	primary.RequestSessionRefresh()
	time.Sleep(300 * time.Millisecond)
	if got := ta.cachedAccounts(); !slices.Contains(got, "x") {
		t.Fatalf("x removed after a single miss: %v", got)
	}
	primary.RequestSessionRefresh()
	waitFor(t, "x removed after two misses", func() bool { return !slices.Contains(ta.cachedAccounts(), "x") })
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", s2 + "~a", s2 + "~x"}) {
		t.Fatalf("cached accounts = %v", got)
	}
	if tr, _ := ta.store().Translation(ta.ctx, s2+"~x", "e1", "en"); tr != "local only" {
		t.Fatalf("s2~x translation = %q, want it kept", tr)
	}
	ta.waitSubjects(s2+"~x", "inbox", "shared on two")

	// 附加登录新得到一个共享账号: 推送里出现会话没有的账号时重读会话并按前缀引导。
	shared3 := ts.two.AddAccount("y", "new@two.test")
	ts.carol.Grant(shared3, false)
	shared3.AddEmail(jmaptest.Email{Subject: "newly shared"})
	ta.waitSubjects(s2+"~y", "inbox", "newly shared")
	if got := ta.cachedAccounts(); slices.Contains(got, "y") {
		t.Fatalf("newly shared account of s2 cached without prefix: %v", got)
	}
	info := ta.signInInfo(s2)
	if !slices.Contains(info.Accounts, s2+"~y") {
		t.Fatalf("ListSignIns accounts of %s = %v", s2, info.Accounts)
	}
}

// 静音设置按本机账号 id 存, 撞车的两个 "a" 互不影响。
func TestMultiSignInMuteIsPerLocalAccount(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	raw, _ := json.Marshal([]string{s2 + "~a"})
	if err := ta.store().SetStringSetting(ta.ctx, "mutedMailAccounts", string(raw)); err != nil {
		t.Fatal(err)
	}
	ts.twoA.AddEmail(jmaptest.Email{Subject: "muted", From: "y@ext.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "loud", From: "x@ext.test"})
	ta.waitSubjects(s2+"~a", "inbox", "hello from two", "muted")
	ta.waitSubjects("a", "inbox", "hello from one", "loud")
	waitFor(t, "notification for the primary's mail", func() bool { return len(ta.notes.all()) > 0 })
	time.Sleep(200 * time.Millisecond)
	for _, n := range ta.notes.all() {
		if n.ActionID == s2+"~a/"+lastID(ts.twoA) {
			t.Fatalf("muted s2~a notified: %+v", n)
		}
	}
	// 静音账号的未读不计入托盘, 主登录的照计。
	if n := ta.a.unreadTotal(); n != 2 {
		t.Fatalf("unread total = %d, want 2 (s2~a muted)", n)
	}
}

func lastID(acc *jmaptest.Account) string {
	list := acc.Emails()
	if len(list) == 0 {
		return ""
	}
	return list[len(list)-1].ID
}

// 场景 1 (直接拼 JMAP 请求的路径): 文件夹、自动回复在撞车的账号 id 下也发到正确的服务器, 账号 id 用服务器的。
func TestMultiSignInDirectRequestsUseServerID(t *testing.T) {
	ts := newTwoServers(t)
	ts.oneA.SetCaps(jmaptest.Mail, jmaptest.Submission, jmaptest.VacationResponse)
	ts.twoA.SetCaps(jmaptest.Mail, jmaptest.Submission, jmaptest.VacationResponse)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"

	if err := ta.a.CreateMailbox(local2, "", "Projects"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ts.twoA.MailboxNames(), "Projects") || slices.Contains(ts.oneA.MailboxNames(), "Projects") {
		t.Fatalf("mailbox created on the wrong server: one=%v two=%v", ts.oneA.MailboxNames(), ts.twoA.MailboxNames())
	}
	waitFor(t, "new folder cached under s2~a", func() bool {
		boxes, _ := ta.store().Mailboxes(ta.ctx, local2)
		for _, b := range boxes {
			if b.Name == "Projects" {
				return true
			}
		}
		return false
	})
	if boxes, _ := ta.store().Mailboxes(ta.ctx, "a"); slices.ContainsFunc(boxes, func(b store.Mailbox) bool { return b.Name == "Projects" }) {
		t.Fatal("s2's new folder cached under the primary's a")
	}

	if err := ta.a.SaveVacation(local2, Vacation{Enabled: true, Subject: "away"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := ts.twoA.Object("VacationResponse", "singleton"); v["isEnabled"] != true {
		t.Fatalf("server two vacation = %v", v)
	}
	if v, ok := ts.oneA.Object("VacationResponse", "singleton"); ok && v["isEnabled"] == true {
		t.Fatal("s2's vacation reply was switched on for the primary")
	}
	got, err := ta.a.GetVacation(local2)
	if err != nil || !got.Enabled || got.Subject != "away" {
		t.Fatalf("GetVacation(%s) = %+v, %v", local2, got, err)
	}
	if got, _ := ta.a.GetVacation("a"); got.Enabled {
		t.Fatal("primary vacation reads as enabled")
	}
	for _, c := range ts.two.Calls() {
		if c.AccountID != "" && c.AccountID != "a" {
			t.Errorf("server two got %s with account %q", c.Method, c.AccountID)
		}
	}
}
