package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestNormalizeAccountLook(t *testing.T) {
	cases := []struct{ in, want AccountLook }{
		{AccountLook{Name: "  Work  ", Color: "blue"}, AccountLook{Name: "Work", Color: "blue"}},
		{AccountLook{Color: "chartreuse"}, AccountLook{}},
		{AccountLook{Name: strings.Repeat("é", 70)}, AccountLook{Name: strings.Repeat("é", 64)}},
	}
	for _, c := range cases {
		if got := normalizeAccountLook(c.in); got != c.want {
			t.Errorf("normalizeAccountLook(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
	if v := encodeAccountLook(AccountLook{}); v != "" {
		t.Errorf("an empty look is a tombstone, got %q", v)
	}
}

// 同步键与本机键互换: 登录前缀每台设备不同, 同步键只认会话地址 + 服务器账号 id (或用户名)。
func TestAccountLooksSyncKeys(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var events []string
	a := &App{ctx: ctx, store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.tap = func(name string, _ ...any) { events = append(events, name) }
	a.primary.cfg = config.Config{SessionEndpoint: "https://Mail.example/jmap/session/", Username: "me@example"}
	a.extra = []*signIn{
		{id: "s2", cfg: config.Config{SessionEndpoint: "https://other.example/jmap", Username: "Gmail@Gateway"}},
		{id: "s3", cfg: config.Config{SessionEndpoint: "https://mail.example/jmap/session", Username: "colleague@example"}},
		{id: "s4", removed: true, cfg: config.Config{SessionEndpoint: "https://gone.example/jmap", Username: "x"}},
	}

	for key, look := range map[string]AccountLook{
		"c":         {Name: "Work", Color: "blue"},
		"s2~a":      {Color: "green"},
		"signin:s2": {Name: "Gmail"},
	} {
		if err := a.SetAccountLook(key, look); err != nil {
			t.Fatalf("SetAccountLook(%q): %v", key, err)
		}
	}
	rows, _ := st.TimedSettingsWithPrefix(ctx, accountLookPrefix)
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	wantKeys := map[string]bool{
		"https://mail.example/jmap/session#c":       true,
		"https://other.example/jmap#a":              true,
		"https://other.example/jmap#@gmail@gateway": true,
	}
	if len(keys) != len(wantKeys) {
		t.Fatalf("sync keys = %v", keys)
	}
	for _, k := range keys {
		if !wantKeys[k] {
			t.Errorf("unexpected sync key %q", k)
		}
	}

	got, err := a.GetAccountLooks()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]AccountLook{
		"c":         {Name: "Work", Color: "blue"},
		"s3~c":      {Name: "Work", Color: "blue"}, // 同一台服务器的另一个登录看到的同一个邮箱
		"s2~a":      {Color: "green"},
		"signin:s2": {Name: "Gmail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetAccountLooks = %v, want %v", got, want)
	}

	// 没变的不重写 (修改时间是同步比新旧的依据), 清除留墓碑。
	events = nil
	if err := a.SetAccountLook("c", AccountLook{Name: " Work ", Color: "blue"}); err != nil || len(events) != 0 {
		t.Errorf("unchanged look was rewritten: %v %v", err, events)
	}
	if err := a.SetAccountLook("c", AccountLook{}); err != nil {
		t.Fatal(err)
	}
	if v, err := st.StringSetting(ctx, accountLookPrefix+"https://mail.example/jmap/session#c"); err != nil || v != "" {
		t.Errorf("cleared look should stay as a tombstone: %q %v", v, err)
	}
	if got, _ := a.GetAccountLooks(); got["c"] != (AccountLook{}) {
		t.Errorf("cleared look still shown: %v", got)
	}

	if err := a.SetAccountLook("s4~a", AccountLook{Name: "x"}); err == nil {
		t.Error("a removed sign-in must be rejected")
	}
}

// 合并逐条按修改时间, 墓碑与普通值一样参与比较。
func TestMergeTimedAccountLooks(t *testing.T) {
	local := map[string]store.TimedSetting{
		"e#a": {Value: `{"name":"A"}`, UpdatedAt: 200},
		"e#b": {Value: "", UpdatedAt: 300},
	}
	remote := map[string]store.TimedSetting{
		"e#a": {Value: `{"name":"A old"}`, UpdatedAt: 100},
		"e#b": {Value: `{"color":"red"}`, UpdatedAt: 100},
		"e#c": {Value: `{"color":"gray"}`, UpdatedAt: 50},
	}
	merged, changes, remoteChanged := mergeTimed(local, remote, nil)
	if !remoteChanged {
		t.Error("local edits must be pushed")
	}
	if !reflect.DeepEqual(changes, map[string]store.TimedSetting{"e#c": remote["e#c"]}) {
		t.Errorf("changes = %v", changes)
	}
	if merged["e#a"].Value != `{"name":"A"}` || merged["e#b"].Value != "" || merged["e#c"].Value != `{"color":"gray"}` {
		t.Errorf("merged = %v", merged)
	}
}

// 收藏按加入的先后排, 同一台服务器的两个登录只列一次, 移出留墓碑。
func TestFavoriteFolders(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{ctx: ctx, store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.tap = func(string, ...any) {}
	a.primary.cfg = config.Config{SessionEndpoint: "https://mail.example/jmap", Username: "me@example"}
	a.extra = []*signIn{
		{id: "s2", cfg: config.Config{SessionEndpoint: "https://other.example/jmap", Username: "gw"}},
		{id: "s3", cfg: config.Config{SessionEndpoint: "https://mail.example/jmap", Username: "colleague@example"}},
	}

	rows := map[string]store.TimedSetting{
		"https://mail.example/jmap#e/inbox":  {Value: "1", UpdatedAt: 300},
		"https://other.example/jmap#a/b1":    {Value: "1", UpdatedAt: 100},
		"https://mail.example/jmap#c/old":    {Value: "", UpdatedAt: 200},
		"https://gone.example/jmap#x/inbox":  {Value: "1", UpdatedAt: 50},
		"https://mail.example/jmap#@me/oops": {Value: "1", UpdatedAt: 60},
	}
	got := localFavoriteFolders(rows, a.lookSignIns())
	want := []FavoriteFolder{{"s2~a", "b1"}, {"e", "inbox"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("localFavoriteFolders = %v, want %v", got, want)
	}

	if err := a.SetFavoriteFolder("s2~a", "b1", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFavoriteFolder("s2~a", "b1", false); err != nil {
		t.Fatal(err)
	}
	if v, err := st.StringSetting(ctx, favoriteFolderPrefix+"https://other.example/jmap#a/b1"); err != nil || v != "" {
		t.Errorf("removed favorite should stay as a tombstone: %q %v", v, err)
	}
	if got, _ := a.GetFavoriteFolders(); len(got) != 0 {
		t.Errorf("removed favorite still listed: %v", got)
	}
	if err := a.SetFavoriteFolder("signin:s2", "b1", true); err == nil {
		t.Error("a sign-in key is not an account")
	}
}
