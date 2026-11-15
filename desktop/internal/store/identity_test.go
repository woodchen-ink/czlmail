package store

import (
	"context"
	"testing"
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
