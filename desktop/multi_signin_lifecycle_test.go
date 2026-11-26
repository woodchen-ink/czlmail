package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 场景 3: 一个登录的凭据被拒 (401)。另一个照常同步, 它的缓存一点没删; 失效提示指向正确的登录; 重新登录后恢复。
func TestMultiSignInCredentialsRejected(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"
	if err := ta.store().SaveTranslation(ta.ctx, local2, "m1", "en", "kept"); err != nil {
		t.Fatal(err)
	}
	rows := ta.countRows(s2)

	ts.carol.Reject(true)
	waitFor(t, "s2 asks to sign in again", func() bool { return ta.signInInfo(s2).SignInRequired })
	if info := ta.signInInfo(""); info.SignInRequired || !info.Connected {
		t.Fatalf("primary affected by s2's rejection: %+v", info)
	}
	if st := ta.a.GetSessionStatus(); st.SignInRequired {
		t.Fatalf("primary session status says sign-in required: %+v", st)
	}
	if !strings.HasPrefix(ta.signInInfo(s2).SignInReason, "2060") {
		t.Fatalf("s2 reason = %q", ta.signInInfo(s2).SignInReason)
	}
	notes := ta.notes.all()
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "carol@two.test") {
		t.Fatalf("sign-in notifications = %+v, want one naming carol", notes)
	}

	// 主登录照常同步; s2 的缓存一点没少。
	ts.oneA.AddEmail(jmaptest.Email{Subject: "still flowing", From: "x@ext.test"})
	ta.waitSubjects("a", "inbox", "hello from one", "still flowing")
	if n := ta.countRows(s2); n != rows {
		t.Fatalf("s2 rows %d -> %d after its credentials were rejected", rows, n)
	}
	if tr, _ := ta.store().Translation(ta.ctx, local2, "m1", "en"); tr != "kept" {
		t.Fatal("s2 translation deleted")
	}
	// 被拒的登录不再连: 服务器上新来的信不会进缓存, 也不会一直重试撞防爆破。
	ts.two.ResetCalls()
	time.Sleep(1500 * time.Millisecond)
	if n := len(ts.two.Calls()); n != 0 {
		t.Fatalf("rejected sign-in kept calling server two: %d calls", n)
	}

	// 重新登录 (同一个人) 后恢复。
	ts.carol.Reject(false)
	if err := ta.a.SignInAgainWithPassword(s2, ts.two.URL(), "carol@two.test", "pw-carol"); err != nil {
		t.Fatal(err)
	}
	if info := ta.signInInfo(s2); info.SignInRequired || !info.Connected {
		t.Fatalf("s2 after signing in again: %+v", info)
	}
	ts.twoA.AddEmail(jmaptest.Email{Subject: "back", From: "y@ext.test"})
	ta.waitSubjects(local2, "inbox", "hello from two", "back")

	// 反过来: 主登录被拒, s2 照常。
	ts.alice.Reject(true)
	waitFor(t, "primary asks to sign in again", func() bool { return ta.a.GetSessionStatus().SignInRequired })
	if info := ta.signInInfo(s2); info.SignInRequired || !info.Connected {
		t.Fatalf("s2 affected by the primary's rejection: %+v", info)
	}
	ts.twoA.AddEmail(jmaptest.Email{Subject: "s2 still flowing", From: "y@ext.test"})
	ta.waitSubjects(local2, "inbox", "hello from two", "back", "s2 still flowing")
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", local2}) {
		t.Fatalf("cached accounts = %v", got)
	}
}

// 场景 3 (退出): 退出一个登录, 另一个照常; 退出的不提示失效、缓存保留、重启后不连; 重新登录后恢复。
func TestMultiSignInSignOutOne(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"
	rows := ta.countRows(s2)

	if err := ta.a.SignOutOf(s2); err != nil {
		t.Fatal(err)
	}
	if info := ta.signInInfo(s2); !info.SignedOut || info.Connected || info.SignInRequired {
		t.Fatalf("s2 after sign-out: %+v", info)
	}
	if pass, _ := config.LoadPassword(ts.two.SessionURL(), "carol@two.test"); pass != "" {
		t.Fatal("s2 password left in the keychain")
	}
	if pass, _ := config.LoadPassword(ts.one.SessionURL(), "alice@one.test"); pass == "" {
		t.Fatal("signing out of s2 deleted the primary's password")
	}
	ts.oneA.AddEmail(jmaptest.Email{Subject: "still flowing", From: "x@ext.test"})
	ta.waitSubjects("a", "inbox", "hello from one", "still flowing")
	if n := ta.countRows(s2); n != rows {
		t.Fatalf("s2 rows %d -> %d after sign-out", rows, n)
	}

	ta = ta.restart()
	waitFor(t, "primary restored", func() bool { return ta.connected("") })
	time.Sleep(300 * time.Millisecond)
	if info := ta.signInInfo(s2); !info.SignedOut || info.Connected || info.SignInRequired {
		t.Fatalf("s2 after restart: %+v", info)
	}
	if ta.events.count(EventSignInRequired) != 0 || len(ta.notes.all()) != 0 {
		t.Fatal("signed-out sign-in prompted to sign in again after restart")
	}
	ta.waitSubjects(local2, "inbox", "hello from two")

	if err := ta.a.SignInAgainWithPassword(s2, ts.two.URL(), "carol@two.test", "pw-carol"); err != nil {
		t.Fatal(err)
	}
	ts.twoA.AddEmail(jmaptest.Email{Subject: "back", From: "y@ext.test"})
	ta.waitSubjects(local2, "inbox", "hello from two", "back")
	if info := ta.signInInfo(s2); info.SignedOut || !info.Connected {
		t.Fatalf("s2 after signing in again: %+v", info)
	}
}

// 场景 4: 一台服务器离线时重启。另一个登录正常恢复; 离线的不清自己的缓存账号, 不提示失效, 服务器回来后自己重连。
func TestMultiSignInRestartWhileOneServerOffline(t *testing.T) {
	ts := newTwoServers(t)
	shared := ts.two.AddAccount("x", "shared@two.test")
	shared.AddEmail(jmaptest.Email{Subject: "shared on two"})
	ts.carol.Grant(shared, false)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	ta.waitSubjects(s2+"~x", "inbox", "shared on two")
	before := ta.cachedAccounts()

	ts.two.SetMode(jmaptest.Down)
	ta = ta.restart()
	waitFor(t, "primary restored", func() bool { return ta.connected("") })
	ts.oneA.AddEmail(jmaptest.Email{Subject: "after restart", From: "x@ext.test"})
	ta.waitSubjects("a", "inbox", "hello from one", "after restart")
	time.Sleep(time.Second)
	if info := ta.signInInfo(s2); info.Connected || info.SignInRequired || info.SignedOut {
		t.Fatalf("offline s2: %+v", info)
	}
	if got := ta.cachedAccounts(); !slices.Equal(got, before) {
		t.Fatalf("cached accounts %v -> %v while s2's server is down", before, got)
	}
	ta.waitSubjects(s2+"~a", "inbox", "hello from two")

	// 服务器回来: 退避重试连上, 不用重启。
	ts.two.SetMode(jmaptest.Online)
	ts.twoA.AddEmail(jmaptest.Email{Subject: "server two is back", From: "y@ext.test"})
	waitForWithin(t, 20*time.Second, "s2 reconnected", func() bool { return ta.connected(s2) })
	ta.waitSubjects(s2+"~a", "inbox", "hello from two", "server two is back")
	if got := ta.cachedAccounts(); !slices.Equal(got, before) {
		t.Fatalf("cached accounts %v -> %v after s2 came back", before, got)
	}
	if ta.events.count(EventSignInRequired) != 0 {
		t.Fatal("an offline server was treated as rejected credentials")
	}

	// 运行中 503: 同步循环退避重连, 不算失效, 恢复后补上。
	ts.one.SetMode(jmaptest.Unavailable)
	time.Sleep(500 * time.Millisecond)
	ts.one.SetMode(jmaptest.Online)
	ts.oneA.AddEmail(jmaptest.Email{Subject: "after 503", From: "x@ext.test"})
	waitForWithin(t, 20*time.Second, "primary caught up after 503", func() bool {
		return slices.Contains(ta.subjects("a", "inbox"), "after 503")
	})
	if ta.a.GetSessionStatus().SignInRequired {
		t.Fatal("503 treated as rejected credentials")
	}
}

func waitForWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// 场景 5: 移除附加登录只删它自己前缀下的缓存、按账号记的设置与钥匙串条目。
func TestMultiSignInRemoveOnlyItsOwnData(t *testing.T) {
	ts := newTwoServers(t)
	dave := ts.two.AddUser("dave@two.test", "pw-dave")
	daveA := ts.two.AddAccount("d", "dave@two.test")
	dave.Grant(daveA, true)
	daveA.AddEmail(jmaptest.Email{Subject: "dave's"})

	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	s3 := ta.addSignIn(ts.two, "dave@two.test", "pw-dave")
	ta.waitSubjects(s3+"~d", "inbox", "dave's")
	st := ta.store()
	for _, acc := range []string{"a", s2 + "~a", s3 + "~d"} {
		if err := st.SaveTranslation(ta.ctx, acc, "m1", "en", "local"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetStringSetting(ta.ctx, "reconciledAt:"+acc, time.Now().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	muted, _ := json.Marshal([]string{"a", s2 + "~a", s3 + "~d"})
	_ = st.SetStringSetting(ta.ctx, "mutedMailAccounts", string(muted))
	_ = st.SetStringSetting(ta.ctx, settingUnifiedExcluded, string(muted))
	defaults, _ := json.Marshal(map[string]string{"a": "a-identity", s2 + "~a": "a-identity", s3 + "~d": "d-identity"})
	_ = st.SetStringSetting(ta.ctx, "defaultIdentities", string(defaults))
	primaryRows, s3Rows := ta.countRows(""), ta.countRows(s3)

	if err := ta.a.RemoveSignIn(s2); err != nil {
		t.Fatal(err)
	}
	if n := ta.countRows(s2); n != 0 {
		t.Fatalf("%d rows left under %s~ after removing it", n, s2)
	}
	if n := ta.countRows(s3); n != s3Rows {
		t.Fatalf("removing %s changed %s's rows %d -> %d", s2, s3, s3Rows, n)
	}
	if got := ta.cachedAccounts(); !slices.Equal(got, []string{"a", s3 + "~d"}) {
		t.Fatalf("cached accounts = %v", got)
	}
	_ = primaryRows
	if tr, _ := st.Translation(ta.ctx, "a", "m1", "en"); tr != "local" {
		t.Fatal("primary translation deleted")
	}
	for _, key := range []string{"mutedMailAccounts", settingUnifiedExcluded} {
		if got := ta.a.accountListSetting(st, key); !slices.Equal(got, []string{"a", s3 + "~d"}) {
			t.Fatalf("%s = %v", key, got)
		}
	}
	raw, _ := st.StringSetting(ta.ctx, "defaultIdentities")
	if strings.Contains(raw, s2+"~") || !strings.Contains(raw, s3+"~d") || !strings.Contains(raw, `"a"`) {
		t.Fatalf("defaultIdentities = %s", raw)
	}
	if v, _ := st.StringSetting(ta.ctx, "reconciledAt:"+s2+"~a"); v != "" {
		t.Fatal("reconcile mark of the removed sign-in kept")
	}
	if v, _ := st.StringSetting(ta.ctx, "reconciledAt:a"); v == "" {
		t.Fatal("primary reconcile mark deleted")
	}
	if pass, _ := config.LoadPassword(ts.two.SessionURL(), "carol@two.test"); pass != "" {
		t.Fatal("removed sign-in's password left in the keychain")
	}
	for _, u := range []struct {
		srv  *jmaptest.Server
		name string
	}{{ts.one, "alice@one.test"}, {ts.two, "dave@two.test"}} {
		if pass, _ := config.LoadPassword(u.srv.SessionURL(), u.name); pass == "" {
			t.Fatalf("removing %s deleted %s's password", s2, u.name)
		}
	}
	f, _ := config.LoadFile()
	if _, ok := f.SignIn(s2); ok {
		t.Fatal("removed sign-in still in config.json")
	}
	// 其余登录照常同步; 前缀不再分配。
	daveA.AddEmail(jmaptest.Email{Subject: "dave again"})
	ta.waitSubjects(s3+"~d", "inbox", "dave's", "dave again")
	if _, err := ta.a.syncerFor(s2 + "~a"); err == nil {
		t.Fatal("removed sign-in still routes")
	}
	s4 := ta.addSignIn(ts.two, "carol@two.test", "pw-carol")
	if s4 == s2 {
		t.Fatalf("prefix %s reused", s2)
	}
	ta.waitSubjects(s4+"~a", "inbox", "hello from two")
	if tr, _ := st.Translation(ta.ctx, s4+"~a", "m1", "en"); tr != "" {
		t.Fatal("new sign-in inherited the removed one's local data")
	}
}

// 场景 5 (竞态): 移除登录时还有一次拉取在途 (界面上「从服务器拉取全部邮件」, 或同步循环的一次增量)。
// 它回来时不能把邮件写回已删掉的前缀下, 留下没有账号行的孤儿数据。
func TestMultiSignInRemoveWhilePullInFlight(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(ta *testApp, ts *twoServers, local string) error
	}{
		{"pull mailbox", func(ta *testApp, ts *twoServers, local string) error {
			return ta.a.PullMailbox(local, "inbox")
		}},
		{"sync loop", func(ta *testApp, ts *twoServers, local string) error {
			ts.twoA.AddEmail(jmaptest.Email{Subject: "racing"})
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTwoServers(t)
			// 比首次引导的窗口 (500 封) 多: 拉取整个文件夹时才有本地没有的旧信要取。
			old := time.Now().Add(-48 * time.Hour)
			for i := range 520 {
				ts.twoA.AddEmail(jmaptest.Email{Subject: "old", ReceivedAt: old.Add(-time.Duration(i) * time.Minute)})
			}
			ta := newTestApp(t)
			ta.signInPrimary(ts.one, "alice@one.test", "pw-alice")
			s2 := ta.addSignIn(ts.two, "carol@two.test", "pw-carol")
			local := s2 + "~a"
			waitFor(t, "s2 bootstrapped", func() bool { return ta.emailCount(local) == 500 })
			waitFor(t, "s2 idle", func() bool { return ts.two.StreamCount(ts.carol) == 1 })
			time.Sleep(200 * time.Millisecond)

			// 卡住下一次 Email/get, 等移除做完再放行。
			blocked, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			ts.two.OnMethod(func(c jmaptest.Call) {
				if c.Method == "Email/get" {
					once.Do(func() { close(blocked); <-release })
				}
			})
			if err := tc.start(ta, ts, local); err != nil {
				t.Fatal(err)
			}
			select {
			case <-blocked:
			case <-time.After(10 * time.Second):
				t.Fatal("no Email/get reached server two")
			}
			if err := ta.a.RemoveSignIn(s2); err != nil {
				t.Fatal(err)
			}
			if n := ta.countRows(s2); n != 0 {
				t.Fatalf("%d rows left right after removal", n)
			}
			close(release)
			time.Sleep(time.Second)
			if n := ta.countRows(s2); n != 0 {
				t.Fatalf("in-flight fetch wrote %d orphan rows under %s~ after the sign-in was removed", n, s2)
			}
		})
	}
}

// 移除正在恢复 (认证途中) 的登录、退出正在恢复的登录: 会话不会被重新建起来 (#26 / #28 修过的竞态, 用真实路径回归)。
func TestMultiSignInRemoveDuringRestore(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)

	ts.two.SetMode(jmaptest.Unavailable)
	ta = ta.restart()
	waitFor(t, "primary restored", func() bool { return ta.connected("") })
	// s2 在退避重试; 移除后服务器回来, 下一次重试必须放弃。
	if err := ta.a.RemoveSignIn(s2); err != nil {
		t.Fatal(err)
	}
	ts.two.SetMode(jmaptest.Online)
	time.Sleep(6 * time.Second)
	if n := ta.countRows(s2); n != 0 {
		t.Fatalf("%d rows written back by a retry after removal", n)
	}
	if ts.two.StreamCount(ts.carol) != 0 {
		t.Fatal("removed sign-in reconnected")
	}
	if _, err := ta.a.signInByID(s2); !errors.Is(err, errUnknownSignIn) {
		t.Fatalf("signInByID(removed) = %v", err)
	}
	_ = store.KindInbox
}

// 退出登录后, 还拿着旧会话的后台拉取不能接着用已删的凭据访问服务器。
func TestMultiSignInSignOutStopsBackgroundPull(t *testing.T) {
	ts := newTwoServers(t)
	old := time.Now().Add(-48 * time.Hour)
	for i := range 1100 {
		ts.twoA.AddEmail(jmaptest.Email{Subject: "old", ReceivedAt: old.Add(-time.Duration(i) * time.Minute)})
	}
	ta := newTestApp(t)
	ta.signInPrimary(ts.one, "alice@one.test", "pw-alice")
	s2 := ta.addSignIn(ts.two, "carol@two.test", "pw-carol")
	local := s2 + "~a"
	waitFor(t, "s2 bootstrapped", func() bool { return ta.emailCount(local) == 500 })
	waitFor(t, "s2 idle", func() bool { return ts.two.StreamCount(ts.carol) == 1 })
	time.Sleep(200 * time.Millisecond)

	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ts.two.OnMethod(func(c jmaptest.Call) {
		if c.Method == "Email/get" {
			once.Do(func() { close(blocked); <-release })
		}
	})
	if err := ta.a.PullMailbox(local, "inbox"); err != nil {
		t.Fatal(err)
	}
	<-blocked
	if err := ta.a.SignOutOf(s2); err != nil {
		t.Fatal(err)
	}
	ts.two.ResetCalls()
	close(release)
	time.Sleep(time.Second)
	if calls := ts.two.Calls(); len(calls) != 0 {
		t.Fatalf("signed-out sign-in kept pulling: %d calls, first %s", len(calls), calls[0].Method)
	}
}

// 退出正在退避重试的登录: 服务器回来后也不再连, 不提示失效。
func TestMultiSignInSignOutDuringRestore(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	ts.two.SetMode(jmaptest.Down)
	ta = ta.restart()
	waitFor(t, "primary restored", func() bool { return ta.connected("") })
	if err := ta.a.SignOutOf(s2); err != nil {
		t.Fatal(err)
	}
	ts.two.SetMode(jmaptest.Online)
	time.Sleep(6 * time.Second)
	if ts.two.StreamCount(ts.carol) != 0 || ta.connected(s2) {
		t.Fatal("signed-out sign-in reconnected")
	}
	if ta.events.count(EventSignInRequired) != 0 || len(ta.notes.all()) != 0 {
		t.Fatal("signed-out sign-in prompted to sign in again")
	}
	if info := ta.signInInfo(s2); !info.SignedOut {
		t.Fatalf("s2 = %+v", info)
	}
	ta.waitSubjects(s2+"~a", "inbox", "hello from two")
}
