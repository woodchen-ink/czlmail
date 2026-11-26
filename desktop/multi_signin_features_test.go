package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 第 5 步 (#15) 的跨登录功能: 写信身份、自己的地址、通知、提醒与 MCP 的账号选择。
// 需要服务器的端到端场景在 multi_signin_integration_test.go (mock JMAP 服务器)。

// multiSignInApp 组装主登录 (账号 c 个人、d 共享) 与附加登录 s2 (s2~c 个人、s2~e 共享)、s3 (已退出) 的 App。
func multiSignInApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, []store.Account{
			{ID: "c", Name: "me@one.example", IsPersonal: true}, {ID: "d", Name: "Support"},
			{ID: "s2~c", Name: "me@two.example", IsPersonal: true}, {ID: "s2~e", Name: "Team"},
			{ID: "s3~x", Name: "old@three.example", IsPersonal: true},
		}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil)), store: st}
	a.primary.cfg = config.Config{ServerHost: "one.example", SessionEndpoint: "https://one.example/jmap", Username: "me@one.example", AuthMethod: config.AuthPassword}
	a.extra = []*signIn{
		{id: "s2", cfg: config.Config{ServerHost: "two.example", SessionEndpoint: "https://two.example/jmap", Username: "Me@Two.example", AuthMethod: config.AuthPassword}},
		{id: "s3", cfg: config.Config{ServerHost: "three.example", SessionEndpoint: "https://three.example/jmap", Username: "old@three.example", SignedOut: true, AuthMethod: config.AuthPassword}, signedOut: true},
	}
	resetOwnAddresses()
	t.Cleanup(resetOwnAddresses)
	return a, st
}

// 发件人下拉: 写信账号在前, 再是其它登录的个人账号; 同一登录的共享邮箱不列。
func TestComposeIdentityAccounts(t *testing.T) {
	a, _ := multiSignInApp(t)
	accounts, err := a.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	ids := func(list []store.Account) []string {
		out := []string{}
		for _, acc := range list {
			out = append(out, acc.ID)
		}
		return out
	}
	for _, tc := range []struct {
		compose string
		want    []string
	}{
		{"c", []string{"c", "s2~c", "s3~x"}},
		// 在共享邮箱里写信 (回复): 共享邮箱自己的身份在前, 主登录的个人账号不另列 (同一登录, 与原来一致)。
		{"d", []string{"d", "s2~c", "s3~x"}},
		{"s2~e", []string{"s2~e", "c", "s3~x"}},
		{"s2~c", []string{"s2~c", "c", "s3~x"}},
	} {
		if got := ids(composeIdentityAccounts(accounts, tc.compose)); !slices.Equal(got, tc.want) {
			t.Errorf("compose %s: got %v, want %v", tc.compose, got, tc.want)
		}
	}
	// 只有一个登录: 只有写信账号这一组, 与原来相同。
	single := []store.Account{{ID: "c", IsPersonal: true}, {ID: "d"}}
	if got := ids(composeIdentityAccounts(single, "c")); !slices.Equal(got, []string{"c"}) {
		t.Errorf("单登录应只有写信账号, 实际 %v", got)
	}
}

type memBlobs struct {
	data    map[string][]byte
	n       int
	uploads []string // 上传到的账号
	fail    error
}

func (m *memBlobs) DownloadBlob(_ context.Context, acc, id string) (io.ReadCloser, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	b, ok := m.data[acc+"/"+id]
	if !ok {
		return nil, errors.New("blob not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memBlobs) UploadBlobType(_ context.Context, acc, ct string, r io.Reader) (string, string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, fmt.Errorf("upload: %w", err)
	}
	m.n++
	id := fmt.Sprintf("new%d", m.n)
	m.data[acc+"/"+id] = b
	m.uploads = append(m.uploads, acc)
	return id, ct, int64(len(b)), nil
}

// 换发件账号时附件 blob 从原账号搬到新账号: 内容、名称、cid 不变, 同一个 blob 只搬一次。
func TestTransferBlobs(t *testing.T) {
	src := &memBlobs{data: map[string][]byte{"c/b1": []byte("pdf"), "c/b2": []byte("png")}}
	dst := &memBlobs{data: map[string][]byte{}}
	refs := []AttachmentRef{
		{BlobID: "b1", Type: "application/pdf", Name: "a.pdf"},
		{BlobID: "b2", Type: "image/png", Name: "logo.png", CID: "logo"},
		{BlobID: "b2", Type: "image/png", Name: "logo.png"},
	}
	got, err := transferBlobs(context.Background(), src, "c", dst, "s2~c", 0, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(dst.uploads) != 2 || dst.uploads[0] != "s2~c" {
		t.Errorf("应往 s2~c 上传两个 blob, 实际 %v", dst.uploads)
	}
	if got[0].BlobID == "b1" || got[1].BlobID != got[2].BlobID || got[1].CID != "logo" || got[0].Name != "a.pdf" {
		t.Errorf("新引用不对: %+v", got)
	}
	if string(dst.data["s2~c/"+got[0].BlobID]) != "pdf" {
		t.Error("内容没搬对")
	}
	if refs[0].BlobID != "b1" {
		t.Error("不应改动传入的引用")
	}

	// 超过目标服务器的上传上限: 2441, 不把文件整个传上去。
	big := &memBlobs{data: map[string][]byte{"c/big": bytes.Repeat([]byte("x"), 100)}}
	_, err = transferBlobs(context.Background(), big, "c", &memBlobs{data: map[string][]byte{}}, "s2~c", 10, []AttachmentRef{{BlobID: "big", Name: "big.bin"}})
	if err == nil || !strings.HasPrefix(err.Error(), "2441 ") {
		t.Errorf("超限应报 2441, 实际 %v", err)
	}
	// 刚好等于上限可以。
	exact := &memBlobs{data: map[string][]byte{"c/x": bytes.Repeat([]byte("x"), 10)}}
	if _, err := transferBlobs(context.Background(), exact, "c", &memBlobs{data: map[string][]byte{}}, "s2~c", 10, []AttachmentRef{{BlobID: "x"}}); err != nil {
		t.Errorf("等于上限应能搬, 实际 %v", err)
	}
	// 原账号下载失败: 2440, 整体失败。
	_, err = transferBlobs(context.Background(), &memBlobs{fail: errors.New("offline")}, "c", dst, "s2~c", 0, refs)
	if err == nil || !strings.HasPrefix(err.Error(), "2440 ") {
		t.Errorf("下载失败应报 2440, 实际 %v", err)
	}
}

func TestDraftInOtherAccount(t *testing.T) {
	for _, tc := range []struct {
		req  ComposeRequest
		want bool
	}{
		{ComposeRequest{AccountID: "c", DraftID: "m1"}, false},
		{ComposeRequest{AccountID: "c", DraftID: "m1", DraftAccountID: "c"}, false},
		{ComposeRequest{AccountID: "s2~c", DraftID: "m1", DraftAccountID: "c"}, true},
	} {
		if got := draftInOtherAccount(tc.req); got != tc.want {
			t.Errorf("%+v: got %v", tc.req, got)
		}
	}
}

// 自己的地址包含所有登录的登录名 (含已退出的登录: 它以前发的信仍是自己的), 不分大小写。
func TestOwnAddressesCoverAllSignIns(t *testing.T) {
	a, _ := multiSignInApp(t)
	own := a.ownAddresses()
	for _, addr := range []string{"me@one.example", "me@two.example", "old@three.example"} {
		if !own[addr] {
			t.Errorf("ownAddresses 缺 %s: %v", addr, own)
		}
	}
	// 身份没取到 (登录都没连上): 只缓存很短的时间, 连上后尽快补上。
	ownCache.Lock()
	ttl := ownCache.ttl
	ownCache.Unlock()
	if ttl != ownCacheRetry {
		t.Errorf("身份没取全时缓存应为 %v, 实际 %v", ownCacheRetry, ttl)
	}
	if got := a.OwnAddresses(); !slices.IsSorted(got) || len(got) != 3 {
		t.Errorf("OwnAddresses = %v", got)
	}
	// 自己 (任一登录) 发的信不弹通知。
	e := &store.Email{From: []store.Address{{Email: "ME@two.example"}}}
	if !isFromSelf(e, own) {
		t.Error("附加登录的登录名发的信应算自己的")
	}
	if !a.isOwnAddress("c", "me@two.example") {
		t.Error("回复时别的登录的地址也是自己的")
	}
}

// 邮件通知的 actionID 带附加登录的前缀: 打开的是那个登录的账号。
func TestMailActionTargetPrefixed(t *testing.T) {
	a, _ := multiSignInApp(t)
	for _, tc := range []struct{ in, acc, id string }{
		{"c/m1", "c", "m1"},
		{"s2~c/m1", "s2~c", "m1"},
	} {
		acc, id, ok := a.mailActionTarget(tc.in)
		if !ok || acc != tc.acc || id != tc.id {
			t.Errorf("%q → %q %q %v", tc.in, acc, id, ok)
		}
	}
	for _, bad := range []string{"", "c", "/m1", "c/"} {
		if _, _, ok := a.mailActionTarget(bad); ok {
			t.Errorf("%q 不应解析成功", bad)
		}
	}
	// czlmail:// 链接里的前缀账号 id 原样保留。
	req, ok := parseMailLink("czlmail://email/s2~c/m1")
	if !ok || req.AccountID != "s2~c" || req.ID != "m1" {
		t.Errorf("parseMailLink = %+v %v", req, ok)
	}
}

// 日程提醒覆盖所有登录 (含离线的, 按缓存), 退出了的登录与已删掉的登录不提醒。
func TestReminderAccounts(t *testing.T) {
	a, _ := multiSignInApp(t)
	got := a.reminderAccounts(a.store)
	want := []string{"c", "d", "s2~c", "s2~e"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("reminderAccounts = %v, want %v", got, want)
	}
	// 只剩主登录退出 (还有别的登录时配置保留并标记 SignedOut): 主登录的日历也不提醒。
	a.primary.signedOut = true
	got = a.reminderAccounts(a.store)
	slices.Sort(got)
	if !slices.Equal(got, []string{"s2~c", "s2~e"}) {
		t.Errorf("主登录退出后 reminderAccounts = %v", got)
	}
}

// MCP 省略账号: 默认主登录的个人账号; list_emails / list_identities 覆盖每个登录的个人账号; list_accounts 标出登录。
func TestMCPAccountsAcrossSignIns(t *testing.T) {
	a, _ := multiSignInApp(t)
	if acc, err := a.mcpAccount(""); err != nil || acc != "c" {
		t.Errorf("默认账号应为主登录的个人账号 c, 实际 %q %v", acc, err)
	}
	if acc, _ := a.mcpAccount("s2~e"); acc != "s2~e" {
		t.Errorf("指定的账号原样使用, 实际 %q", acc)
	}
	personal, err := a.mcpPersonalAccounts()
	if err != nil || !slices.Equal(personal, []string{"c", "s2~c", "s3~x"}) {
		t.Errorf("mcpPersonalAccounts = %v %v", personal, err)
	}
	all, err := a.mcpAccounts("")
	if err != nil || len(all) != 5 {
		t.Errorf("search 省略账号应覆盖全部 5 个账号, 实际 %v %v", all, err)
	}

	out := a.mcpAccountList()
	if out["defaultAccountId"] != "c" {
		t.Errorf("defaultAccountId = %v", out["defaultAccountId"])
	}
	accs := out["accounts"].([]store.Account)
	for _, acc := range accs {
		if acc.ID == "s2~e" && acc.SignIn != "s2" {
			t.Errorf("s2~e 应标出属于 s2, 实际 %q", acc.SignIn)
		}
	}
	signIns := out["signIns"].([]map[string]any)
	if len(signIns) != 3 || signIns[1]["id"] != "s2" || signIns[1]["server"] != "two.example" || signIns[2]["signedOut"] != true {
		t.Errorf("signIns = %v", signIns)
	}
}

// 取身份在锁外并行、限时: 一个账号卡住不拖住别的调用方; 取不到的账号沿用上次的身份; 过期后先返回旧结果。
func TestOwnAddressesParallelAndStale(t *testing.T) {
	a, _ := multiSignInApp(t)
	old := fetchOwnIdentities
	t.Cleanup(func() { fetchOwnIdentities = old })

	var mu sync.Mutex
	offline := false
	fetchOwnIdentities = func(_ *App, ctx context.Context, acc string) ([]string, error) {
		mu.Lock()
		down := offline
		mu.Unlock()
		if acc == "s2~e" && down {
			<-ctx.Done() // 服务器连不上: 等到超时
			return nil, ctx.Err()
		}
		return []string{"id-" + strings.ReplaceAll(acc, "~", "-") + "@x"}, nil
	}
	own := a.ownAddresses()
	if !own["id-s2-e@x"] || !own["id-c@x"] || !own["me@two.example"] {
		t.Fatalf("第一次应取全: %v", own)
	}

	// s2 的服务器连不上: 下一次刷新时它的身份沿用上次的, 其余照常; 期间调用方立刻拿到旧结果。
	mu.Lock()
	offline = true
	mu.Unlock()
	forgetOwnAddresses()
	start := time.Now()
	if got := a.ownAddresses(); !got["id-s2-e@x"] {
		t.Error("过期后应先返回旧结果")
	}
	if time.Since(start) > time.Second {
		t.Errorf("已有结果时不应等刷新, 用了 %v", time.Since(start))
	}
	// 等后台刷新结束 (卡住的那个账号要等到 ownFetchTimeout)。
	deadline := time.Now().Add(ownFetchTimeout + 5*time.Second)
	for {
		ownCache.Lock()
		done := !ownCache.refreshing && !ownCache.at.IsZero()
		ttl := ownCache.ttl
		ownCache.Unlock()
		if done {
			if ttl != ownCacheRetry {
				t.Errorf("有账号没取到时应短缓存, 实际 %v", ttl)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("后台刷新没结束")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := a.ownAddresses(); !got["id-s2-e@x"] || !got["id-c@x"] {
		t.Errorf("取不到的账号应沿用上次的身份: %v", got)
	}

	// 主登录换人: 连上次的结果一起丢掉。
	resetOwnAddresses()
	ownCache.Lock()
	empty := ownCache.addrs == nil && ownCache.perAccount == nil
	ownCache.Unlock()
	if !empty {
		t.Error("resetOwnAddresses 应清掉旧结果")
	}
}

// 收件人目录合并各登录的邮局目录; 查询失败有负缓存, 连不上的服务器不会每敲一个字重试; 正在查的不重复查也不等。
func TestDirectoryAcrossSignIns(t *testing.T) {
	a, _ := multiSignInApp(t)
	// 会话里没有这两个账号, 能力按未知处理 (不算缺成员目录)。
	a.primary.client = &jmap.Client{SessionEndpoint: "https://one.example/jmap", Session: &jmap.Session{}}
	a.primary.sync = syncer.New(a.primary.client, a.store, a.log)
	a.extra[0].client = &jmap.Client{SessionEndpoint: "https://two.example/jmap", Session: &jmap.Session{}}
	a.extra[0].sync = syncer.New(a.extra[0].client, a.store, a.log)
	a.extra[0].sync.SetSignIn("s2")
	directoryCache.Lock()
	directoryCache.entries = nil
	directoryCache.Unlock()
	old := fetchDirectory
	t.Cleanup(func() {
		fetchDirectory = old
		directoryCache.Lock()
		directoryCache.entries = nil
		directoryCache.Unlock()
	})

	var mu sync.Mutex
	calls := map[string]int{}
	release := make(chan struct{})
	fetchDirectory = func(_ *App, ctx context.Context, rt accountRoute) ([]store.Recipient, error) {
		mu.Lock()
		calls[rt.client.SessionEndpoint]++
		n := calls[rt.client.SessionEndpoint]
		mu.Unlock()
		if strings.Contains(rt.client.SessionEndpoint, "two") {
			if n == 1 {
				<-release // 第一次查得慢
				return []store.Recipient{{Email: "bob@two.example"}, {Email: "Alice@one.example"}}, nil
			}
			return nil, errors.New("unreachable")
		}
		return []store.Recipient{{Email: "alice@one.example", Name: "Alice"}}, nil
	}

	// s2 正在查 (慢): 另一次调用不等它, 先拿到主登录的。
	go a.directory()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		started := calls["https://two.example/jmap"] == 1
		mu.Unlock()
		if started || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	got := a.directory()
	if time.Since(start) > time.Second || len(got) != 1 || got[0].Email != "alice@one.example" {
		t.Fatalf("s2 查询在路上时应立刻返回主登录的目录, 实际 %v (%v)", got, time.Since(start))
	}
	close(release)
	time.Sleep(100 * time.Millisecond)

	got = a.directory()
	emails := []string{}
	for _, r := range got {
		emails = append(emails, r.Email)
	}
	if !slices.Equal(emails, []string{"alice@one.example", "bob@two.example"}) {
		t.Errorf("合并结果 = %v (主登录在前, 同一地址只列一次)", emails)
	}

	// s2 过期后再查失败: 沿用上次的结果, 1 分钟内不再查。
	directoryCache.Lock()
	for k, e := range directoryCache.entries {
		if strings.Contains(k, "two") {
			e.at = time.Now().Add(-directoryTTL - time.Second)
		}
	}
	directoryCache.Unlock()
	for range 5 {
		got = a.directory()
	}
	mu.Lock()
	n := calls["https://two.example/jmap"]
	mu.Unlock()
	if n != 2 {
		t.Errorf("失败后应负缓存, s2 实际查了 %d 次", n)
	}
	if len(got) != 2 {
		t.Errorf("查询失败时应沿用上次的目录, 实际 %v", got)
	}
}
