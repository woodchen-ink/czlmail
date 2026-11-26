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

// 移除附加登录按前缀删: 账号行已不在的孤儿数据一起删; 主登录与前缀相近的别的登录 (s22) 不动。
func TestRemoveSignInData(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	mail := []Email{{ID: "m1", Subject: "hello", ReceivedAt: time.Unix(1700000005, 0)}}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertAccounts(ctx, tx, []Account{{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}, {ID: "s22~c", IsPersonal: true}}, nil); err != nil {
			return err
		}
		for _, acc := range []string{"c", "s2~c", "s2~gone", "s22~c"} {
			if err := UpsertEmails(ctx, tx, acc, mail); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"reconciledAt:s2~c", "reconciledAt:s22~c", "reconciledAt:c"} {
		if err := s.SetStringSetting(ctx, k, "1"); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.RemoveSignInData(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
	for acc, want := range map[string]bool{"c": true, "s2~c": false, "s2~gone": false, "s22~c": true} {
		if _, err := s.Email(ctx, acc, "m1"); (err == nil) != want {
			t.Errorf("mail of %s kept=%v, want %v", acc, err == nil, want)
		}
	}
	accs, _ := s.Accounts(ctx)
	if len(accs) != 2 {
		t.Errorf("accounts = %+v, want c and s22~c", accs)
	}
	for k, want := range map[string]bool{"reconciledAt:s2~c": false, "reconciledAt:s22~c": true, "reconciledAt:c": true} {
		if v, _ := s.StringSetting(ctx, k); (v != "") != want {
			t.Errorf("%s kept=%v, want %v", k, v != "", want)
		}
	}
	if err := s.RemoveSignInData(ctx, ""); err == nil {
		t.Error("an empty prefix must be refused: it would match the primary sign-in")
	}
}
