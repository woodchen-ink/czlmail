package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// 附件名随元数据落库并出现在摘要里; 后续没带结构的更新 (nil) 不能把它清掉。
// 没取过的 (NULL) 由补齐流程找出, 补齐时服务器没返回的写空数组, 不再被找出。
func TestAttachmentNames(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	upsert := func(msgs ...Email) {
		t.Helper()
		if err := s.WithTx(ctx, func(tx *sql.Tx) error { return UpsertEmails(ctx, tx, "acc", msgs) }); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Unix(1700000000, 0)
	upsert(
		Email{ID: "a", MailboxIDs: []string{"inbox"}, ReceivedAt: at, HasAttachment: true, AttachmentNames: []string{"invoice.pdf", "photo.jpg"}},
		Email{ID: "b", MailboxIDs: []string{"inbox"}, ReceivedAt: at.Add(-time.Minute), HasAttachment: true},
		Email{ID: "c", MailboxIDs: []string{"inbox"}, ReceivedAt: at.Add(-2 * time.Minute), HasAttachment: true},
		Email{ID: "d", MailboxIDs: []string{"inbox"}, ReceivedAt: at.Add(-3 * time.Minute), Subject: "Your verification code is 482913"},
	)
	// 标记已读之类的更新也走 upsert; 这里模拟一次不带结构树的写入。
	upsert(Email{ID: "a", MailboxIDs: []string{"inbox"}, ReceivedAt: at, HasAttachment: true})

	list, err := s.EmailsByMailbox(ctx, "acc", "inbox", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]EmailSummary{}
	for _, e := range list {
		byID[e.ID] = e
	}
	if got := strings.Join(byID["a"].AttachmentNames, ","); got != "invoice.pdf,photo.jpg" {
		t.Fatalf("a attachments = %q", got)
	}
	if byID["d"].Code != "482913" {
		t.Fatalf("d code = %q", byID["d"].Code)
	}

	missing, err := s.EmailsMissingAttachmentNames(ctx, "acc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(missing, ","); got != "b,c" {
		t.Fatalf("missing = %q", got)
	}
	if err := s.SetAttachmentNames(ctx, "acc", missing, map[string][]string{"b": {"report.xlsx"}}); err != nil {
		t.Fatal(err)
	}
	if missing, _ = s.EmailsMissingAttachmentNames(ctx, "acc", 10); len(missing) != 0 {
		t.Fatalf("still missing %v", missing)
	}
}
