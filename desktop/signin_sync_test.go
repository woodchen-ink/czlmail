package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncseal"
)

func testSyncKey(t *testing.T) []byte {
	t.Helper()
	_, key, err := syncseal.New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

var gatewaySignIn = syncedSignIn{
	Server: "gw.example.net", AuthMethod: config.AuthPassword,
	SessionEndpoint: "https://gw.example.net/.well-known/jmap", Username: "user@example.net",
}

const gatewayKey = "https://gw.example.net/.well-known/jmap#@user@example.net"

func noPassword(string) string { return "" }

func TestSignInSyncKeyMatchesAccountLooks(t *testing.T) {
	cfg := gatewaySignIn.config()
	cfg.SessionEndpoint = "https://GW.example.net/.well-known/jmap/"
	cfg.Username = "User@Example.net"
	if got := signInSyncKey(cfg); got != gatewayKey {
		t.Fatalf("key = %q", got)
	}
}

func TestPlanImportsRemoteSignInWithPassword(t *testing.T) {
	key := testSyncKey(t)
	sealed, _ := syncseal.Seal(key, gatewayKey, "app-pass")
	s := gatewaySignIn
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &s, Password: sealed, UpdatedAt: isoTime(100)}}

	plan := planSignInSync(map[string]store.TimedSetting{}, remote, key, noPassword)
	if len(plan.actions) != 1 || plan.actions[0].removed || plan.actions[0].password != "app-pass" {
		t.Fatalf("actions = %+v", plan.actions)
	}
	if plan.write {
		t.Fatal("nothing new to upload")
	}
	if plan.changes[gatewayKey].UpdatedAt != 100 {
		t.Fatalf("local record not taken from remote: %+v", plan.changes)
	}
}

func TestPlanUploadsLocalSignIn(t *testing.T) {
	key := testSyncKey(t)
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 200}}
	plan := planSignInSync(local, nil, key, func(string) string { return "app-pass" })
	if !plan.write || len(plan.actions) != 0 {
		t.Fatalf("write = %v, actions = %+v", plan.write, plan.actions)
	}
	e := plan.out[gatewayKey]
	if pw, err := syncseal.Open(key, gatewayKey, e.Password); err != nil || pw != "app-pass" {
		t.Fatalf("uploaded password = %q, %v", pw, err)
	}
}

func TestPlanTombstoneRemovesOlderLocal(t *testing.T) {
	key := testSyncKey(t)
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 100}}
	remote := map[string]signInSyncEntry{gatewayKey: {UpdatedAt: isoTime(200)}}
	plan := planSignInSync(local, remote, key, noPassword)
	if len(plan.actions) != 1 || !plan.actions[0].removed {
		t.Fatalf("actions = %+v", plan.actions)
	}
	if plan.out[gatewayKey].SignIn != nil || plan.out[gatewayKey].Password != "" {
		t.Fatalf("tombstone carries data: %+v", plan.out[gatewayKey])
	}
}

func TestPlanNewerLocalReAddBeatsTombstone(t *testing.T) {
	key := testSyncKey(t)
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 300}}
	remote := map[string]signInSyncEntry{gatewayKey: {UpdatedAt: isoTime(200)}}
	plan := planSignInSync(local, remote, key, noPassword)
	if len(plan.actions) != 0 || !plan.write || plan.out[gatewayKey].SignIn == nil {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanPasswordChangeOnlyIsApplied(t *testing.T) {
	// 别的设备重新登录换了密码: 配置没变, 只有时间与密文变了, 本机也要拿到新密码。
	key := testSyncKey(t)
	sealed, _ := syncseal.Seal(key, gatewayKey, "new-pass")
	s := gatewaySignIn
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 100}}
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &s, Password: sealed, UpdatedAt: isoTime(200)}}
	plan := planSignInSync(local, remote, key, func(string) string { return "old-pass" })
	if len(plan.actions) != 1 || plan.actions[0].password != "new-pass" {
		t.Fatalf("actions = %+v", plan.actions)
	}
	if plan.write {
		t.Fatal("remote already newest")
	}
	if plan.out[gatewayKey].Password != sealed {
		t.Fatal("remote password replaced by the older local one")
	}
}

func TestPlanSteadyStateIsQuiet(t *testing.T) {
	key := testSyncKey(t)
	sealed, _ := syncseal.Seal(key, gatewayKey, "app-pass")
	s := gatewaySignIn
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 100}}
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &s, Password: sealed, UpdatedAt: isoTime(100)}}
	plan := planSignInSync(local, remote, key, func(string) string { return "app-pass" })
	if plan.write || len(plan.actions) != 0 || len(plan.changes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanFillsMissingRemotePassword(t *testing.T) {
	key := testSyncKey(t)
	s := gatewaySignIn
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 100}}
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &s, UpdatedAt: isoTime(100)}}
	plan := planSignInSync(local, remote, key, func(string) string { return "app-pass" })
	if !plan.write || plan.out[gatewayKey].Password == "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanOAuthCarriesNoPassword(t *testing.T) {
	key := testSyncKey(t)
	s := gatewaySignIn
	s.AuthMethod = config.AuthOAuth
	s.Issuer, s.ClientID = "https://idp.example.net", "client"
	local := map[string]store.TimedSetting{gatewayKey: {Value: s.encode(), UpdatedAt: 100}}
	plan := planSignInSync(local, nil, key, func(string) string { return "should-not-upload" })
	if plan.out[gatewayKey].Password != "" {
		t.Fatal("oauth sign-in uploaded a password")
	}
}

func TestPlanEqualTimesConverge(t *testing.T) {
	// 同一秒里两台设备写了不同内容: 两边按同一规则选出同一个。
	key := testSyncKey(t)
	a := gatewaySignIn
	b := gatewaySignIn
	b.Server = "other.example.net"
	planA := planSignInSync(map[string]store.TimedSetting{gatewayKey: {Value: a.encode(), UpdatedAt: 100}},
		map[string]signInSyncEntry{gatewayKey: {SignIn: &b, UpdatedAt: isoTime(100)}}, key, noPassword)
	planB := planSignInSync(map[string]store.TimedSetting{gatewayKey: {Value: b.encode(), UpdatedAt: 100}},
		map[string]signInSyncEntry{gatewayKey: {SignIn: &a, UpdatedAt: isoTime(100)}}, key, noPassword)
	if planA.out[gatewayKey].SignIn.Server != planB.out[gatewayKey].SignIn.Server {
		t.Fatal("devices disagree on the winner")
	}
}

func TestPlanIgnoresForgedEndpoint(t *testing.T) {
	// 能改网盘文件的人把服务器地址换成自己的, 想骗设备把密码发过去: 配置对不上同步键的条目不导入。
	key := testSyncKey(t)
	sealed, _ := syncseal.Seal(key, gatewayKey, "app-pass")
	forged := gatewaySignIn
	forged.SessionEndpoint = "https://evil.example.net/.well-known/jmap"
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &forged, Password: sealed, UpdatedAt: isoTime(100)}}
	plan := planSignInSync(nil, remote, key, noPassword)
	if len(plan.actions) != 0 {
		t.Fatalf("forged entry imported: %+v", plan.actions)
	}
	if _, kept := plan.out[gatewayKey]; kept || !plan.write {
		t.Fatalf("forged entry should be dropped from the file: %+v", plan)
	}
}

func TestPlanKeepsUnknownEntries(t *testing.T) {
	// 以后的版本加的登录方式: 读不懂不等于移除, 原样留在文件里。
	key := testSyncKey(t)
	future := gatewaySignIn
	future.AuthMethod = "passkey"
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &future, UpdatedAt: isoTime(100)}}
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 50}}
	plan := planSignInSync(local, remote, key, noPassword)
	if len(plan.actions) != 0 || len(plan.changes) != 0 {
		t.Fatalf("unknown entry acted on: %+v", plan)
	}
	if got := plan.out[gatewayKey]; got.SignIn == nil || got.SignIn.AuthMethod != "passkey" {
		t.Fatalf("unknown entry not kept: %+v", got)
	}
}

func TestPlanPreexistingSignInDefersToRemote(t *testing.T) {
	// 开启同步前就有的登录记成最早: 网盘里已有的 (可能是更新的密码) 胜出, 本机的旧密码不往外推。
	key := testSyncKey(t)
	sealed, _ := syncseal.Seal(key, gatewayKey, "new-pass")
	s := gatewaySignIn
	local := map[string]store.TimedSetting{gatewayKey: {Value: gatewaySignIn.encode(), UpdatedAt: 1}}
	remote := map[string]signInSyncEntry{gatewayKey: {SignIn: &s, Password: sealed, UpdatedAt: isoTime(100)}}
	plan := planSignInSync(local, remote, key, func(string) string { return "old-pass" })
	if plan.write || plan.out[gatewayKey].Password != sealed {
		t.Fatalf("old local password pushed: %+v", plan)
	}
}

func TestFindSignInBySyncKey(t *testing.T) {
	f := config.File{Config: config.Config{AuthMethod: config.AuthPassword, SessionEndpoint: "https://mail.example.net/.well-known/jmap", Username: "me@example.net"}}
	cfg := gatewaySignIn.config()
	f.SignIns = []config.SignIn{{ID: "s2", Config: cfg}}
	if id, ok := findSignInBySyncKey(f, gatewayKey); !ok || id != "s2" {
		t.Fatalf("extra = %q, %v", id, ok)
	}
	if id, ok := findSignInBySyncKey(f, signInSyncKey(f.Config)); !ok || id != "" {
		t.Fatalf("primary = %q, %v", id, ok)
	}
}

// 两台设备: A 加了附加登录并开启同步; B 只登录主账号, 输入同步口令后附加登录自动加上并连通;
// B 移除它, 墓碑写回网盘。
func TestSignInSyncAcrossDevices(t *testing.T) {
	ts := newTwoServers(t)
	// 同步文件存在主登录的个人网盘里。
	ts.oneA.SetCaps(append(slices.Clone(jmaptest.DefaultCaps), jmaptest.FileNode)...)
	ta := newTestApp(t)
	t.Cleanup(func() { _ = ta.a.DisableSignInSync() })
	ts.signIn(ta)
	waitFor(t, "personal files synced on device A", func() bool {
		st, _ := ta.a.GetSignInSync()
		return st.State != "loading"
	})

	if st, err := ta.a.GetSignInSync(); err != nil || st.State != "off" {
		t.Fatalf("status before enabling = %+v, %v", st, err)
	}
	if err := ta.a.EnableSignInSync("short"); !errors.Is(err, syncseal.ErrShortPassphrase) {
		t.Fatalf("short passphrase: %v", err)
	}
	if err := ta.a.EnableSignInSync("correct horse"); err != nil {
		t.Fatal(err)
	}
	if st, err := ta.a.GetSignInSync(); err != nil || st.State != "on" || len(st.Pending) != 0 {
		t.Fatalf("status on device A = %+v, %v", st, err)
	}
	ta.stop()

	// 换一台设备: 新的数据目录与缓存库, 钥匙串里没有同步密钥, 也没有附加登录的密码。
	if err := ta.a.DisableSignInSync(); err != nil {
		t.Fatal(err)
	}
	if err := config.DeletePassword(ts.two.SessionURL(), "carol@two.test"); err != nil {
		t.Fatal(err)
	}
	tb := newTestApp(t)
	tb.signInPrimary(ts.one, "alice@one.test", "pw-alice")
	tb.waitSubjects("a", "inbox", "hello from one")
	waitFor(t, "personal files synced on device B", func() bool {
		st, _ := tb.a.GetSignInSync()
		return st.State != "loading"
	})
	st, err := tb.a.GetSignInSync()
	if err != nil || st.State != "locked" || len(st.Pending) != 1 || !strings.HasPrefix(st.Pending[0], "carol@two.test") {
		t.Fatalf("status on device B = %+v, %v", st, err)
	}
	if err := tb.a.EnableSignInSync("wrong horse!"); !errors.Is(err, syncseal.ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if err := tb.a.EnableSignInSync("correct horse"); err != nil {
		t.Fatal(err)
	}
	var s2 string
	waitFor(t, "synced sign-in connects on device B", func() bool {
		list, _ := tb.a.ListSignIns()
		for _, s := range list {
			if s.Username == "carol@two.test" && s.Connected {
				s2 = s.ID
				return true
			}
		}
		return false
	})
	tb.waitSubjects(s2+"~a", "inbox", "hello from two")

	// 再同步一次不会重复添加。
	if err := tb.a.SyncSignIns(); err != nil {
		t.Fatal(err)
	}
	if list, _ := tb.a.ListSignIns(); len(list) != 2 {
		t.Fatalf("sign-ins after a second sync = %+v", list)
	}

	if err := tb.a.RemoveSignIn(s2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "tombstone uploaded", func() bool {
		_ = tb.a.SyncSignIns()
		driveSyncMu.Lock()
		defer driveSyncMu.Unlock()
		_, data, err := tb.a.openDriveSyncFile(tb.ctx, signInSyncFileName)
		if err != nil {
			return false
		}
		f, err := parseSignInSyncFile(data)
		e, ok := f.SignIns[signInSyncKey(config.Config{SessionEndpoint: ts.two.SessionURL(), Username: "carol@two.test"})]
		return err == nil && ok && e.SignIn == nil && e.Password == ""
	})
}
