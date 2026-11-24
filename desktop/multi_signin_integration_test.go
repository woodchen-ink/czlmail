package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/sieve"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 多个登录同时连着时, 本机账号 id 与服务器账号 id 的换算 (#15)。
//
// 需要两个登录: CZLMAIL_TEST_HOST/_USER/_PASS 作主登录, CZLMAIL_TEST2_HOST/_USER/_PASS 作附加登录 s2,
// 最好是另一台服务器上账号 id 相同的用户 (Stalwart 的第一个用户都是 "c")。
// 可选 CZLMAIL_TEST3_*: 与主登录同一台服务器的另一个用户 (s3), 它把一个邮箱共享给主登录时,
// 同一个服务器账号会以两个本机账号 id 出现 (主登录里的共享账号与 s3 的个人账号)。
// 测试会在这些账号里建删文件夹、改过滤器与自动回复、存删草稿, 只能指向测试服务器。

type testLogin struct {
	id     string
	client *jmap.Client
}

func dialTestLogin(t *testing.T, prefix, id string) *testLogin {
	t.Helper()
	host, user, pass := os.Getenv(prefix+"_HOST"), os.Getenv(prefix+"_USER"), os.Getenv(prefix+"_PASS")
	if host == "" {
		return nil
	}
	endpoint, err := auth.SessionEndpoint(host)
	if err != nil {
		t.Fatal(err)
	}
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatalf("%s: %v", prefix, err)
	}
	return &testLogin{id: id, client: client}
}

func personalServerID(c *jmap.Client) string {
	for id, acc := range c.Session.Accounts {
		if acc.IsPersonal {
			return string(id)
		}
	}
	return ""
}

func TestIntegrationMultipleSignIns(t *testing.T) {
	first := dialTestLogin(t, "CZLMAIL_TEST", accountid.Primary)
	second := dialTestLogin(t, "CZLMAIL_TEST2", "s2")
	if first == nil || second == nil {
		t.Skip("set CZLMAIL_TEST_* and CZLMAIL_TEST2_* to run")
	}
	third := dialTestLogin(t, "CZLMAIL_TEST3", "s3")
	logins := []*testLogin{first, second}
	if third != nil {
		logins = append(logins, third)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "multi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st}
	for _, l := range logins {
		si := &a.primary
		if l.id != accountid.Primary {
			si = &signIn{id: l.id}
			a.extra = append(a.extra, si)
		}
		si.client = l.client
		si.sync = syncer.New(l.client, st, log)
		si.sync.SetSignIn(l.id)
		if err := a.persistAccounts(ctx, si, l.client, st); err != nil {
			t.Fatal(err)
		}
		for _, acc := range si.sync.Accounts() {
			if err := si.sync.SyncAccount(ctx, acc); err != nil {
				t.Fatalf("sync %s: %v", acc, err)
			}
		}
	}

	local := func(l *testLogin) string { return accountid.Join(l.id, personalServerID(l.client)) }
	acc1, acc2 := local(first), local(second)
	if personalServerID(first.client) == personalServerID(second.client) {
		t.Logf("both personal accounts are %q on their servers: the collision case", personalServerID(first.client))
	}

	// 缓存里每个账号的收件箱与它自己服务器上的一致 (邮件 id 与主题都对得上), 没有串到同 id 的另一个账号里。
	// 不同服务器上的邮件 id 也可能相同, 所以连主题一起比。
	for _, l := range logins {
		for id := range l.client.Session.Accounts {
			acc := accountid.Join(l.id, string(id))
			want := serverInbox(t, ctx, l.client, string(id))
			got := cachedInbox(t, ctx, st, acc)
			if len(got) != len(want) {
				t.Errorf("%s: %d cached inbox emails, %d on its server", acc, len(got), len(want))
			}
			for e, subject := range got {
				if w, ok := want[e]; !ok || w != subject {
					t.Errorf("%s: cached email %s %q does not match its server (%q)", acc, e, subject, w)
				}
			}
			t.Logf("%s: %d inbox emails match the server", acc, len(got))
		}
	}

	// route 找到的是对的会话, 服务器账号 id 去掉了前缀。
	rt, err := a.route(acc2)
	if err != nil || rt.client != second.client || rt.serverID != personalServerID(second.client) {
		t.Fatalf("route(%s) = %+v, %v", acc2, rt, err)
	}

	// 文件夹 (直接发 JMAP 请求, 带 serverID; 随后的同步用本机 id) 只落在附加登录的服务器上。
	name := "czlmail-multi-" + time.Now().Format("150405")
	if err := a.CreateMailbox(acc2, "", name); err != nil {
		t.Fatalf("create mailbox on %s: %v", acc2, err)
	}
	box := findMailbox(t, ctx, st, acc2, name)
	if box == "" {
		t.Fatal("new folder not synced into the second sign-in's account")
	}
	if findMailbox(t, ctx, st, acc1, name) != "" || serverHasMailbox(t, ctx, first.client, personalServerID(first.client), name) {
		t.Error("folder created for the second sign-in showed up in the primary account")
	}
	if !serverHasMailbox(t, ctx, second.client, personalServerID(second.client), name) {
		t.Error("folder missing on the second server")
	}
	if err := a.DeleteMailbox(acc2, box, false); err != nil {
		t.Errorf("delete mailbox: %v", err)
	}

	// 过滤器: SieveScript/get 用 serverID, 下载脚本内容走同步器、用本机 id。
	before, err := a.GetFilters(acc2)
	if err != nil {
		t.Fatalf("get filters: %v", err)
	}
	rule := sieve.Rule{ID: "multi", Name: name, Enabled: true, MatchType: "all",
		Conditions: []sieve.Condition{{Field: "subject", Comparator: "contains", Value: sieve.StringOrArr{name}}},
		Actions:    []sieve.Action{{Type: "mark_read"}}}
	if err := a.SaveFilters(acc2, []sieve.Rule{rule}); err != nil {
		t.Fatalf("save filters: %v", err)
	}
	got, err := a.GetFilters(acc2)
	if err != nil || !got.Managed || len(got.Rules) != 1 || got.Rules[0].Name != name {
		t.Errorf("filters did not round-trip on %s: %+v, %v", acc2, got, err)
	}
	if other, err := a.GetFilters(acc1); err != nil || strings.Contains(other.Script, name) {
		t.Errorf("filter for the second sign-in leaked into the primary account: %v", err)
	}
	if before.Script != "" {
		if err := a.SaveSieveScript(acc2, before.Script); err != nil {
			t.Errorf("restore filters: %v", err)
		}
	}

	// 自动回复: 改附加登录的, 主登录的不变。
	v1, err := a.GetVacation(acc1)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := a.GetVacation(acc2)
	if err != nil {
		t.Fatal(err)
	}
	changed := v2
	changed.Subject = name
	if err := a.SaveVacation(acc2, changed); err != nil {
		t.Fatalf("save vacation: %v", err)
	}
	if g, _ := a.GetVacation(acc2); g.Subject != name {
		t.Errorf("vacation subject on %s = %q", acc2, g.Subject)
	}
	if g, _ := a.GetVacation(acc1); g.Subject != v1.Subject {
		t.Errorf("vacation of the primary account changed to %q", g.Subject)
	}
	_ = a.SaveVacation(acc2, v2)

	// 上传与下载 blob: URL 模板里填服务器账号 id。
	payload := []byte("czlmail multi sign-in " + name)
	blobID, _, _, err := rt.sync.UploadBlob(ctx, acc2, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("upload blob: %v", err)
	}
	rc, err := rt.sync.DownloadBlob(ctx, acc2, blobID)
	if err != nil {
		t.Fatalf("download blob: %v", err)
	}
	back, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(back, payload) {
		t.Errorf("blob round trip: %q", back)
	}

	// 草稿与正文: 身份、Email/set、Email/get 都在附加登录的服务器上。
	ids, err := a.ListIdentities(acc2)
	if err != nil || len(ids) == 0 {
		t.Fatalf("identities of %s: %v %v", acc2, ids, err)
	}
	draft, err := a.SaveDraft(ComposeRequest{AccountID: acc2, IdentityID: ids[0].ID,
		To: []store.Address{{Email: ids[0].Email}}, Subject: name, TextBody: "draft"})
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	d, err := a.FetchBody(acc2, draft)
	if err != nil || d.Subject != name {
		t.Errorf("fetch draft body: %+v %v", d, err)
	}
	if err := a.DiscardDraft(acc2, draft); err != nil {
		t.Errorf("discard draft: %v", err)
	}

	// 同一个服务器账号经两个登录出现 (主登录里的共享账号, s3 的个人账号): 两个本机 id 各走各的会话。
	if third != nil {
		shared := personalServerID(third.client)
		if _, ok := first.client.Session.Accounts[jmap.ID(shared)]; ok {
			viaPrimary, err1 := a.route(shared)
			viaOwn, err2 := a.route(accountid.Join("s3", shared))
			if err1 != nil || err2 != nil || viaPrimary.client != first.client || viaOwn.client != third.client ||
				viaPrimary.serverID != shared || viaOwn.serverID != shared {
				t.Errorf("shared account routes: %+v %v / %+v %v", viaPrimary, err1, viaOwn, err2)
			}
		} else {
			t.Log("CZLMAIL_TEST3 shares nothing with the primary user; skipping the shared-account check")
		}
	}

	// 清理失去访问的账号只看自己的: 连做两次 (确认次数) 也不能删别的登录的缓存。
	before2, _ := st.Accounts(ctx)
	for range 2 {
		for _, si := range a.signInsLocked() {
			if removed := si.sync.PruneAccounts(ctx); len(removed) > 0 {
				t.Errorf("%q pruned %v", si.id, removed)
			}
		}
	}
	after, _ := st.Accounts(ctx)
	if len(after) != len(before2) {
		t.Errorf("accounts before prune %d, after %d", len(before2), len(after))
	}
}

func serverInbox(t *testing.T, ctx context.Context, c *jmap.Client, account string) map[string]string {
	t.Helper()
	req := &jmap.Request{Context: ctx}
	req.Invoke(&mailbox.Get{Account: jmap.ID(account)})
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.Responses[0].Args.(*mailbox.GetResponse)
	if !ok {
		return nil
	}
	inbox := ""
	for _, b := range got.List {
		if b.Role == mailbox.RoleInbox {
			inbox = string(b.ID)
		}
	}
	if inbox == "" {
		return nil
	}
	out, err := jmapInboxSubjects(ctx, c, account, inbox)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func cachedInbox(t *testing.T, ctx context.Context, st *store.Store, account string) map[string]string {
	t.Helper()
	boxes, err := st.Mailboxes(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, b := range boxes {
		if b.Role != "inbox" {
			continue
		}
		list, err := st.EmailsByMailbox(ctx, account, b.ID, 200, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range list {
			out[e.ID] = e.Subject
		}
	}
	return out
}

func findMailbox(t *testing.T, ctx context.Context, st *store.Store, account, name string) string {
	t.Helper()
	boxes, err := st.Mailboxes(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range boxes {
		if b.Name == name {
			return b.ID
		}
	}
	return ""
}

func serverHasMailbox(t *testing.T, ctx context.Context, c *jmap.Client, account, name string) bool {
	t.Helper()
	req := &jmap.Request{Context: ctx}
	req.Invoke(&mailbox.Get{Account: jmap.ID(account)})
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := resp.Responses[0].Args.(*mailbox.GetResponse)
	if got == nil {
		return false
	}
	for _, b := range got.List {
		if b.Name == name {
			return true
		}
	}
	return false
}

func jmapInboxSubjects(ctx context.Context, c *jmap.Client, account, inbox string) (map[string]string, error) {
	req := &jmap.Request{Context: ctx}
	q := req.Invoke(&email.Query{Account: jmap.ID(account), Filter: &email.FilterCondition{InMailbox: jmap.ID(inbox)}, Limit: 500})
	req.Invoke(&email.Get{Account: jmap.ID(account), Properties: []string{"id", "subject"},
		ReferenceIDs: &jmap.ResultReference{ResultOf: q, Name: "Email/query", Path: "/ids"}})
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if g, ok := resp.Responses[1].Args.(*email.GetResponse); ok {
		for _, e := range g.List {
			out[string(e.ID)] = e.Subject
		}
	}
	return out, nil
}
