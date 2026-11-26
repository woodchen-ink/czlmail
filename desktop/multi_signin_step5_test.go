package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 第 5 步 (#15) 的端到端场景: App 走真实路径, 对着两台内存 JMAP 服务器 (账号 id 都是 a)。

// 写信跨登录: 发件人下拉列出另一台服务器的身份; 换过去后附件搬到那台服务器, 草稿存进新账号并删掉原账号的旧草稿,
// 发信只走到所选身份的服务器。
func TestMultiSignInComposeAcrossServers(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"
	a := ta.a

	groups, err := a.ListComposeIdentities("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].AccountID != "a" || groups[1].AccountID != local2 ||
		groups[1].Username != "carol@two.test" || len(groups[1].Identities) != 1 || groups[1].Identities[0].Email != "carol@two.test" {
		t.Fatalf("compose identity groups = %+v", groups)
	}
	carol := groups[1].Identities[0]
	alice := groups[0].Identities[0]

	// 先在主登录里写: 附件传到服务器一, 存一份草稿。
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("quarterly numbers"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploaded, err := a.AttachPaths("a", []string{path})
	if err != nil || len(uploaded) != 1 {
		t.Fatalf("attach on server one: %v %v", uploaded, err)
	}
	refs := []AttachmentRef{{BlobID: uploaded[0].BlobID, Type: uploaded[0].Type, Name: uploaded[0].Name}}
	req := ComposeRequest{
		AccountID: "a", IdentityID: alice.ID, To: []store.Address{{Email: "z@ext.test"}},
		Subject: "cross sign-in", TextBody: "see attached", Attachments: refs,
	}
	draftOne, err := a.SaveDraft(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.oneA.EmailByID(draftOne); !ok {
		t.Fatal("draft not on server one")
	}

	// 换成 carol 的身份: 附件搬到服务器二, 内容不变。
	moved, err := a.TransferAttachments("a", local2, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].Name != "report.txt" {
		t.Fatalf("moved = %+v", moved)
	}
	s, err := a.syncerFor(local2)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.DownloadBlob(ta.ctx, local2, moved[0].BlobID)
	if err != nil {
		t.Fatalf("moved blob not on server two: %v", err)
	}
	body, _ := io.ReadAll(rc)
	rc.Close()
	if string(body) != "quarterly numbers" {
		t.Fatalf("moved blob content = %q", body)
	}

	// 下一次保存: 存进服务器二, 删掉服务器一的旧草稿。
	ts.one.ResetCalls()
	req.AccountID, req.IdentityID, req.Attachments = local2, carol.ID, moved
	req.DraftID, req.DraftAccountID = draftOne, "a"
	draftTwo, err := a.SaveDraft(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.twoA.EmailByID(draftTwo); !ok {
		t.Fatal("new draft not on server two")
	}
	if _, ok := ts.oneA.EmailByID(draftOne); ok {
		t.Fatal("old draft left on server one")
	}
	if !callsMention(ts.two.CallsTo("Email/set"), moved[0].BlobID) {
		t.Error("draft on server two should reference the moved blob")
	}

	// 发出: 只走服务器二, 发件人是 carol; 服务器一不收到任何发信。
	req.DraftID, req.DraftAccountID = draftTwo, local2
	if err := a.SendEmail(req); err != nil {
		t.Fatal(err)
	}
	subs := ts.two.Submissions()
	if len(subs) != 1 || subs[0].User != "carol@two.test" || subs[0].AccountID != "a" || !slices.Contains(subs[0].From, "carol@two.test") {
		t.Fatalf("server two submissions = %+v", subs)
	}
	if len(ts.one.Submissions()) != 0 {
		t.Fatalf("server one got a submission: %+v", ts.one.Submissions())
	}
	if _, ok := ts.twoA.EmailByID(draftTwo); ok {
		t.Error("draft on server two should be replaced by the sent mail")
	}
	ta.waitSubjects(local2, "sent", "cross sign-in")
}

func callsMention(calls []jmaptest.Call, s string) bool {
	for _, c := range calls {
		if strings.Contains(string(c.Args), s) {
			return true
		}
	}
	return false
}

// 自己的地址覆盖两台服务器: alice 发到 carol 收件箱的信、carol 发到 alice 收件箱的信都不通知 (都是自己的)。
func TestMultiSignInMailBetweenOwnSignInsNotNotified(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	if own := ta.a.ownAddresses(); !own["alice@one.test"] || !own["carol@two.test"] {
		t.Fatalf("ownAddresses = %v", own)
	}
	ts.twoA.AddEmail(jmaptest.Email{Subject: "from alice", From: "alice@one.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "from carol", From: "carol@two.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "marker", From: "x@ext.test"})
	ta.waitSubjects(s2+"~a", "inbox", "hello from two", "from alice")
	waitFor(t, "notification for the marker", func() bool { return len(ta.notes.all()) > 0 })
	time.Sleep(300 * time.Millisecond)
	for _, n := range ta.notes.all() {
		if n.Title == "alice@one.test" || n.Title == "carol@two.test" {
			t.Fatalf("notified of mail between own sign-ins: %+v", n)
		}
	}
	if got := ta.a.OwnAddresses(); !slices.Contains(got, "carol@two.test") {
		t.Errorf("OwnAddresses = %v", got)
	}
}

// MCP 不指定账号时覆盖所有登录: list_emails 合并两边的个人账号, search_emails 两台服务器都搜, 结果带本机账号 id。
func TestMultiSignInMCPAcrossServers(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	local2 := s2 + "~a"
	ts.oneA.AddEmail(jmaptest.Email{Subject: "invoice 42 (one)", From: "billing@ext.test"})
	ts.twoA.AddEmail(jmaptest.Email{Subject: "invoice 42 (two)", From: "billing@ext.test"})
	ta.waitSubjects("a", "inbox", "hello from one", "invoice 42 (one)")
	ta.waitSubjects(local2, "inbox", "hello from two", "invoice 42 (two)")

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := ta.a.newMCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			b, _ := json.Marshal(res.Content)
			t.Fatalf("%s failed: %s", name, b)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	pairs := func(list any) []string {
		var out []string
		for _, e := range list.([]any) {
			m := e.(map[string]any)
			out = append(out, m["accountId"].(string)+"|"+m["subject"].(string))
		}
		slices.Sort(out)
		return out
	}

	accounts := call("list_accounts", map[string]any{})
	if accounts["defaultAccountId"] != "a" || len(accounts["signIns"].([]any)) != 2 {
		t.Fatalf("list_accounts = %v", accounts)
	}

	got := pairs(call("list_emails", map[string]any{})["emails"])
	want := []string{"a|hello from one", "a|invoice 42 (one)", local2 + "|hello from two", local2 + "|invoice 42 (two)"}
	if !slices.Equal(got, want) {
		t.Fatalf("list_emails without account = %v, want %v", got, want)
	}

	ts.one.ResetCalls()
	ts.two.ResetCalls()
	got = pairs(call("search_emails", map[string]any{"query": "invoice 42"})["results"])
	if !slices.Equal(got, []string{"a|invoice 42 (one)", local2 + "|invoice 42 (two)"}) {
		t.Fatalf("search_emails = %v", got)
	}
	if len(ts.one.CallsTo("Email/query")) == 0 || len(ts.two.CallsTo("Email/query")) == 0 {
		t.Error("search should query both servers")
	}

	// 一台服务器离线 (只按发件人筛, 本机缓存兜底不了): 搜索照样返回另一台的结果, 并说明哪个账号没搜到。
	ts.two.SetMode(jmaptest.Unavailable)
	res := call("search_emails", map[string]any{"from": "billing"})
	if got := pairs(res["results"]); !slices.Contains(got, "a|invoice 42 (one)") {
		t.Fatalf("search with server two down = %v", got)
	}
	if skipped, _ := res["skipped"].([]any); len(skipped) != 1 || skipped[0].(map[string]any)["accountId"] != local2 {
		t.Errorf("skipped = %v, want %s", res["skipped"], local2)
	}
}
