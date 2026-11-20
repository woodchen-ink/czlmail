package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// 换人登录时清空上一个人的缓存: 邮件与搜索索引都要清干净, 设备偏好保留。
func TestResetIdentityData(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)
	if err := s.SetStringSetting(ctx, "mutedMailAccounts", `["c"]`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStringSetting(ctx, "reconciledAt:c", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStringSetting(ctx, "markRead", "never"); err != nil {
		t.Fatal(err)
	}

	if err := s.ResetIdentityData(ctx, []string{"mutedMailAccounts"}); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got, err := s.SearchEmails(ctx, "c", "发票申请", 20); err != nil || len(got) != 0 {
		t.Fatalf("search after reset = %v, %v; want empty", got, err)
	}
	for _, k := range []string{"mutedMailAccounts", "reconciledAt:c"} {
		if v, _ := s.StringSetting(ctx, k); v != "" {
			t.Fatalf("%s = %q after reset, want cleared", k, v)
		}
	}
	if v, _ := s.StringSetting(ctx, "markRead"); v != "never" {
		t.Fatalf("device preference markRead = %q, want kept", v)
	}
	// 清空后照常写入与搜索 (FTS 外部内容表没有失配)。
	seedForSearch(t, s)
	if got, err := s.SearchEmails(ctx, "c", "发票申请", 20); err != nil || len(got) != 1 {
		t.Fatalf("search after reseed = %v, %v; want 1", got, err)
	}
}

// 共享被撤销时只删那一个账号: 它的邮件与索引清干净, 其它账号不受影响。
func TestRemoveAccount(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)
	shared := []Email{{ID: "e1", Subject: "增值税发票申请归档", ReceivedAt: time.Unix(1700000005, 0)}}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertAccounts(ctx, tx, []Account{{ID: "c", Name: "me", IsPersonal: true}, {ID: "s", Name: "archive"}}, nil); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "s", shared)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStringSetting(ctx, "reconciledAt:s", "1"); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveAccount(ctx, "s"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if got, err := s.SearchEmails(ctx, "s", "发票申请", 20); err != nil || len(got) != 0 {
		t.Fatalf("removed account search = %v, %v; want empty", got, err)
	}
	if got, err := s.SearchEmails(ctx, "c", "发票申请", 20); err != nil || len(got) != 1 {
		t.Fatalf("other account search = %v, %v; want 1 hit", got, err)
	}
	accs, err := s.Accounts(ctx)
	if err != nil || len(accs) != 1 || accs[0].ID != "c" {
		t.Fatalf("accounts = %v, %v; want only c", accs, err)
	}
	if v, _ := s.StringSetting(ctx, "reconciledAt:s"); v != "" {
		t.Fatalf("reconciledAt:s = %q, want cleared", v)
	}
}
