package syncer

import (
	"context"
	"testing"
	"time"
)

// 引导窗口之外的邮箱(典型如归档)翻页时应从服务端补齐, 整夹拉取应把本地数量补到服务端总数。
func TestIntegrationFillAndPull(t *testing.T) {
	s, cleanup := newTestSyncer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, acc := range s.Accounts() {
		if err := s.SyncAccount(ctx, acc); err != nil {
			t.Fatalf("sync %s: %v", acc, err)
		}
	}

	// 找本地覆盖最差的邮箱: 服务端有邮件而本地缺得最多的那个。
	var accountID, mailboxID, name string
	var gap int64
	for _, acc := range s.Accounts() {
		boxes, err := s.store.Mailboxes(ctx, acc)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range boxes {
			local, total, err := s.store.MailboxCoverage(ctx, acc, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			// 限制规模, 免得测试把几万封的收件箱整个拉一遍。
			if total-local > gap && total <= 3000 {
				accountID, mailboxID, name, gap = acc, b.ID, b.Name, total-local
			}
		}
	}
	if gap == 0 {
		t.Skip("every mailbox is already fully cached")
	}

	local, total, _ := s.store.MailboxCoverage(ctx, accountID, mailboxID)
	t.Logf("mailbox %q: local %d / total %d", name, local, total)

	if err := s.FillMailboxPage(ctx, accountID, mailboxID, 0, 50); err != nil {
		t.Fatalf("fill page: %v", err)
	}
	page, err := s.store.EmailsByMailbox(ctx, accountID, mailboxID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := min(int(total), 50); len(page) < want {
		t.Fatalf("first page after fill has %d emails, want %d", len(page), want)
	}

	var last PullProgress
	if err := s.PullMailbox(ctx, accountID, mailboxID, func(p PullProgress) { last = p }); err != nil {
		t.Fatalf("pull: %v", err)
	}
	local, total, _ = s.store.MailboxCoverage(ctx, accountID, mailboxID)
	t.Logf("after pull: local %d / total %d, progress %+v", local, total, last)
	if local < total {
		t.Fatalf("local %d < total %d after full pull", local, total)
	}
}
