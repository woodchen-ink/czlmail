package syncer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 集成测试打真实服务器, 默认跳过。凭据只从环境变量读, 不落仓库:
//
//	CZLMAIL_TEST_SESSION=https://mail.example.net/.well-known/jmap
//	CZLMAIL_TEST_USER=user@example.net
//	CZLMAIL_TEST_PASS=<app password>
func newTestSyncer(t *testing.T) (*Syncer, func()) {
	t.Helper()

	endpoint := os.Getenv("CZLMAIL_TEST_SESSION")
	user := os.Getenv("CZLMAIL_TEST_USER")
	pass := os.Getenv("CZLMAIL_TEST_PASS")
	if endpoint == "" || user == "" || pass == "" {
		t.Skip("set CZLMAIL_TEST_SESSION / _USER / _PASS to run integration tests")
	}

	client := &jmap.Client{SessionEndpoint: endpoint}
	client.WithBasicAuth(user, pass)
	if err := client.Authenticate(); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(client, st, log), func() { st.Close() }
}

// 首次同步走引导路径, 应拉回邮箱树与最近一个窗口的邮件, 并记录 state token。
func TestIntegrationBootstrap(t *testing.T) {
	s, cleanup := newTestSyncer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	accounts := s.Accounts()
	if len(accounts) == 0 {
		t.Fatal("session reported no accounts")
	}
	t.Logf("accounts: %v", accounts)

	// 个人账号是 mail 能力的 primary account。
	accountID := string(s.client.Session.PrimaryAccounts[jmap.URI("urn:ietf:params:jmap:mail")])
	if accountID == "" {
		t.Fatal("no primary mail account")
	}

	if err := s.SyncAccount(ctx, accountID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	boxes, err := s.store.Mailboxes(ctx, accountID)
	if err != nil {
		t.Fatalf("read mailboxes: %v", err)
	}
	if len(boxes) == 0 {
		t.Fatal("no mailboxes after bootstrap")
	}
	for _, b := range boxes {
		t.Logf("mailbox %-20s role=%-10s total=%-6d unread=%d", b.Name, b.Role, b.TotalEmails, b.UnreadEmails)
	}

	var emailCount int
	if err := s.store.DB().QueryRow(
		`SELECT COUNT(*) FROM emails WHERE account_id = ?`, accountID,
	).Scan(&emailCount); err != nil {
		t.Fatalf("count emails: %v", err)
	}
	t.Logf("emails cached: %d", emailCount)
	if emailCount == 0 {
		t.Error("no emails after bootstrap")
	}

	mailState, err := s.store.State(ctx, accountID, store.TypeEmail)
	if err != nil || mailState == "" {
		t.Fatalf("email state not recorded: %q err=%v", mailState, err)
	}
	t.Logf("email state token: %s", mailState)

	// 第二次同步走增量路径。此时无变化, 应当既不报错也不重复插入。
	if err := s.SyncAccount(ctx, accountID); err != nil {
		t.Fatalf("incremental sync: %v", err)
	}

	var after int
	if err := s.store.DB().QueryRow(
		`SELECT COUNT(*) FROM emails WHERE account_id = ?`, accountID,
	).Scan(&after); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if after != emailCount {
		t.Errorf("email count changed on no-op incremental sync: %d -> %d", emailCount, after)
	}
}

// 共享邮箱与个人邮箱走同一条路径, 逐个账号验证不会因权限差异炸掉。
func TestIntegrationAllAccounts(t *testing.T) {
	s, cleanup := newTestSyncer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	for _, accountID := range s.Accounts() {
		name := s.client.Session.Accounts[jmap.ID(accountID)].Name
		if err := s.SyncAccount(ctx, accountID); err != nil {
			t.Errorf("account %s (%s): %v", accountID, name, err)
			continue
		}

		var n int
		s.store.DB().QueryRow(
			`SELECT COUNT(*) FROM emails WHERE account_id = ?`, accountID,
		).Scan(&n)
		t.Logf("account %s (%s): %d emails", accountID, name, n)
	}
}
