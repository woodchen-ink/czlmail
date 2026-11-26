package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 两个登录: 主登录是公布全部能力的服务器 (Stalwart), s2 是只有邮件能力的服务器 (如经 JMAP 代理接入的 Gmail)。
var (
	fullCaps = map[jmap.URI]json.RawMessage{
		"urn:ietf:params:jmap:mail":             json.RawMessage(`{}`),
		"urn:ietf:params:jmap:submission":       json.RawMessage(`{"maxDelayedSend":2592000}`),
		"urn:ietf:params:jmap:vacationresponse": json.RawMessage(`{}`),
		"urn:ietf:params:jmap:sieve":            json.RawMessage(`{}`),
		"urn:ietf:params:jmap:calendars":        json.RawMessage(`{}`),
		"urn:ietf:params:jmap:calendars:parse":  json.RawMessage(`{}`),
		"urn:ietf:params:jmap:contacts":         json.RawMessage(`{}`),
		"urn:ietf:params:jmap:contacts:parse":   json.RawMessage(`{}`),
		"urn:ietf:params:jmap:filenode":         json.RawMessage(`{}`),
		"urn:ietf:params:jmap:principals":       json.RawMessage(`{}`),
	}
	mailOnlyCaps = map[jmap.URI]json.RawMessage{
		"urn:ietf:params:jmap:mail":       json.RawMessage(`{}`),
		"urn:ietf:params:jmap:submission": json.RawMessage(`{"maxDelayedSend":0}`),
	}
)

func capabilityTestApp(t *testing.T) (*App, *signIn) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx,
			[]store.Account{{ID: "c", Name: "wood", IsPersonal: true}, {ID: "s2~g", Name: "gmail", IsPersonal: true}},
			map[string]map[jmap.URI]json.RawMessage{"c": fullCaps, "s2~g": mailOnlyCaps})
	}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &App{ctx: ctx, log: log, store: st}
	// 客户端没有会话端点: 真的发出请求会以连接错误失败, 断言里能区分「请求前就被拒」。
	full := &jmap.Client{Session: &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"c": {IsPersonal: true, RawCapabilities: fullCaps}}}}
	mail := &jmap.Client{Session: &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"g": {IsPersonal: true, RawCapabilities: mailOnlyCaps}}}}
	a.primary.client, a.primary.sync = full, syncer.New(full, st, log)
	s2 := syncer.New(mail, st, log)
	s2.SetSignIn("s2")
	extra := &signIn{id: "s2", client: mail, sync: s2}
	a.extra = []*signIn{extra}
	return a, extra
}

// TestListAccountsFeatures 界面拿到的账号带各自服务器的功能: 只有邮件的登录只剩发信。
func TestListAccountsFeatures(t *testing.T) {
	a, _ := capabilityTestApp(t)
	list, err := a.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]capability.Features{}
	for _, acc := range list {
		got[acc.ID] = acc.Features
	}
	wantFull := capability.All()
	wantFull.MaxDelayedSend = 2592000
	if got["c"] != wantFull {
		t.Errorf("primary features = %+v, want %+v", got["c"], wantFull)
	}
	if got["s2~g"] != (capability.Features{Submission: true}) {
		t.Errorf("mail-only features = %+v", got["s2~g"])
	}
}

// TestUnsupportedFeaturesRefused 缺能力的账号在发请求前就以 2280 拒绝 (客户端没有服务器, 真发出去会是别的错误); 有能力的账号不拦。
func TestUnsupportedFeaturesRefused(t *testing.T) {
	a, _ := capabilityTestApp(t)
	var unsupported *capability.UnsupportedError
	is2280 := func(err error) bool { return errors.As(err, &unsupported) }

	checks := map[string]func(acc string) error{
		"filters":  func(acc string) error { _, err := a.GetFilters(acc); return err },
		"sieve":    func(acc string) error { return a.SaveSieveScript(acc, "keep;") },
		"vacation": func(acc string) error { _, err := a.GetVacation(acc); return err },
		"save vacation": func(acc string) error {
			return a.SaveVacation(acc, Vacation{Enabled: true})
		},
		"mcp calendar": func(acc string) error { _, _, err := a.mcpCalendar(acc, ""); return err },
		"mcp files":    a.requireFiles,
		"import ics": func(acc string) error {
			_, err := a.importPIMData(acc, "CalendarEvent", []byte("BEGIN:VCALENDAR"), nil)
			return err
		},
	}
	for name, run := range checks {
		if err := run("s2~g"); !is2280(err) {
			t.Errorf("%s on mail-only account: err = %v, want 2280", name, err)
		}
	}
	// 公布了全部能力的账号不拦 (真去请求要服务器, 交给 jmaptest 场景测试)。
	for _, uri := range []jmap.URI{"urn:ietf:params:jmap:sieve", "urn:ietf:params:jmap:vacationresponse", "urn:ietf:params:jmap:calendars",
		"urn:ietf:params:jmap:calendars:parse", "urn:ietf:params:jmap:contacts", "urn:ietf:params:jmap:filenode"} {
		if err := a.requireCapability("c", uri); err != nil {
			t.Errorf("full account refused %s: %v", uri, err)
		}
	}
}

// TestOfflineKeepsLastKnownFeatures 登录离线 (没有会话) 时: 界面照用缓存里上次的能力, 后端不因为离线拦功能。
func TestOfflineKeepsLastKnownFeatures(t *testing.T) {
	a, extra := capabilityTestApp(t)
	extra.client, extra.sync = nil, nil
	list, err := a.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range list {
		if acc.ID == "s2~g" && (acc.Features.Calendars || !acc.Features.Submission) {
			t.Errorf("offline sign-in lost its last known features: %+v", acc.Features)
		}
	}
	// 离线时不按能力拦: 交给后续调用按未登录 / 离线报错。
	if err := a.requireCalendars("s2~g"); err != nil {
		t.Errorf("requireCalendars offline = %v, want nil", err)
	}
}
