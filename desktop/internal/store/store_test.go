package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 迁移必须可重复执行: 第二次打开同一文件不得重跑 DDL。
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	s1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	var v int
	if err := s2.DB().QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
}

// 元数据的重复同步不得清掉已缓存的正文 —— 标记已读这类操作会反复触发元数据更新。
func TestUpsertEmailsPreservesBody(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	msg := Email{
		ID: "e1", ThreadID: "t1", Subject: "hello", BlobID: "b1",
		From:       []Address{{Name: "wood", Email: "user@example.net"}},
		ReceivedAt: time.Unix(1700000000, 0),
		MailboxIDs: []string{"mb1"},
		Keywords:   []string{"$seen"},
	}

	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertEmails(ctx, tx, "c", []Email{msg}); err != nil {
			return err
		}
		return SetEmailBody(ctx, tx, "c", "e1", "plain text", "<p>html</p>", "{}")
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 模拟一次只改标志位的元数据同步。
	msg.Keywords = []string{"$seen", "$flagged"}
	msg.Subject = "hello (updated)"
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertEmails(ctx, tx, "c", []Email{msg})
	}); err != nil {
		t.Fatalf("resync: %v", err)
	}

	var text, html string
	var fetchedAt sql.NullInt64
	var subject string
	if err := s.DB().QueryRow(
		`SELECT subject, COALESCE(body_text,''), COALESCE(body_html,''), body_fetched_at
		 FROM emails WHERE account_id = ? AND id = ?`, "c", "e1",
	).Scan(&subject, &text, &html, &fetchedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if subject != "hello (updated)" {
		t.Errorf("subject = %q, want the resynced value", subject)
	}
	if text != "plain text" || html != "<p>html</p>" {
		t.Errorf("body was clobbered by metadata resync: text=%q html=%q", text, html)
	}
	if !fetchedAt.Valid {
		t.Error("body_fetched_at was reset by metadata resync")
	}
}

// 关键字与邮箱归属是集合语义, 取消标记后本地必须同步消失。
func TestUpsertEmailsReplacesSets(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	msg := Email{
		ID: "e1", ReceivedAt: time.Unix(1700000000, 0),
		MailboxIDs: []string{"mb1", "mb2"},
		Keywords:   []string{"$seen", "$flagged"},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertEmails(ctx, tx, "c", []Email{msg})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 取消星标并移出 mb2。
	msg.MailboxIDs = []string{"mb1"}
	msg.Keywords = []string{"$seen"}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertEmails(ctx, tx, "c", []Email{msg})
	}); err != nil {
		t.Fatalf("resync: %v", err)
	}

	var kw, mb int
	if err := s.DB().QueryRow(
		`SELECT (SELECT COUNT(*) FROM email_keywords  WHERE account_id='c' AND email_id='e1'),
		        (SELECT COUNT(*) FROM email_mailboxes WHERE account_id='c' AND email_id='e1')`,
	).Scan(&kw, &mb); err != nil {
		t.Fatalf("count: %v", err)
	}
	if kw != 1 {
		t.Errorf("keywords = %d, want 1 (stale keyword not removed)", kw)
	}
	if mb != 1 {
		t.Errorf("mailbox links = %d, want 1 (stale mailbox link not removed)", mb)
	}
}

// 首次同步时不存在 state 记录, 必须返回空串而非报错, 调用方据此决定走全量引导。
func TestStateEmptyBeforeFirstSync(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	got, err := s.State(ctx, "c", TypeEmail)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if got != "" {
		t.Errorf("state = %q, want empty", got)
	}

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return SetState(ctx, tx, "c", TypeEmail, "s1")
	}); err != nil {
		t.Fatalf("set state: %v", err)
	}

	if got, _ = s.State(ctx, "c", TypeEmail); got != "s1" {
		t.Errorf("state = %q, want s1", got)
	}
}

// 顶层邮箱的 parentId 必须落 NULL, 否则空串会被当作真实父节点参与建树。
func TestTopLevelMailboxParentIsNull(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertMailboxes(ctx, tx, "c", []Mailbox{
			{ID: "mb1", Name: "Inbox", Role: "inbox", MyRights: map[string]bool{"mayRead": true}},
			{ID: "mb2", Name: "Sub", ParentID: "mb1", MyRights: map[string]bool{}},
		})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var nulls int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM mailboxes WHERE account_id='c' AND parent_id IS NULL`,
	).Scan(&nulls); err != nil {
		t.Fatalf("count: %v", err)
	}
	if nulls != 1 {
		t.Errorf("mailboxes with NULL parent = %d, want 1", nulls)
	}

	boxes, err := s.Mailboxes(ctx, "c")
	if err != nil {
		t.Fatalf("read mailboxes: %v", err)
	}
	if len(boxes) != 2 {
		t.Fatalf("mailboxes = %d, want 2", len(boxes))
	}
}

// FTS5 是否可用决定本地全文搜索能不能做, 纯 Go 驱动未必编进该扩展。
func TestFTS5Availability(t *testing.T) {
	s := openTemp(t)
	_, err := s.DB().Exec(`CREATE VIRTUAL TABLE fts_probe USING fts5(body)`)
	if err != nil {
		t.Skipf("FTS5 unavailable in modernc.org/sqlite: %v", err)
	}
	t.Log("FTS5 available")
}
