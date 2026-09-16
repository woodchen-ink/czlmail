package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func seedForSearch(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	msgs := []Email{
		{ID: "e1", Subject: "增值税发票申请", Preview: "请开具本月发票", ReceivedAt: time.Unix(1700000003, 0)},
		{ID: "e2", Subject: "Quarterly invoice attached", Preview: "please find the invoice", ReceivedAt: time.Unix(1700000002, 0)},
		{ID: "e3", Subject: "服务器告警", Preview: "CPU 使用率过高", ReceivedAt: time.Unix(1700000001, 0)},
	}

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertEmails(ctx, tx, "c", msgs); err != nil {
			return err
		}
		return SetEmailBody(ctx, tx, "c", "e3", "磁盘空间不足, 请尽快清理", "", "{}")
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// trigram 分词器必须能对中文做子串匹配 —— 默认的 unicode61 做不到。
func TestSearchChineseSubstring(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	got, err := s.SearchEmails(ctx, "c", "发票申请", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e1" {
		t.Fatalf("搜索「发票申请」应只命中 e1, 实际 %d 条: %+v", len(got), ids(got))
	}
}

// 正文要参与检索, 且只有已缓存正文的邮件能靠正文命中。
func TestSearchMatchesCachedBody(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	got, err := s.SearchEmails(ctx, "c", "磁盘空间", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e3" {
		t.Fatalf("正文检索应命中 e3, 实际 %v", ids(got))
	}
}

// 英文照常工作。
func TestSearchEnglish(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	got, err := s.SearchEmails(ctx, "c", "invoice", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e2" {
		t.Fatalf("英文检索应命中 e2, 实际 %v", ids(got))
	}
}

// 短查询走前缀路径, 不得因 trigram 的三字下限返回空集。
func TestSearchShortQueryFallsBack(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	got, err := s.SearchEmails(ctx, "c", "告警", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "e3" {
		t.Fatalf("两字查询应回落到子串匹配并命中 e3, 实际 %v", ids(got))
	}
}

// 用户输入里的 FTS5 语法字符必须按字面处理, 不能变成查询语法或语法错误。
func TestSearchQuotesAreLiteral(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	if _, err := s.SearchEmails(ctx, "c", `invoice" OR "x`, 20); err != nil {
		t.Fatalf("含引号的查询不应报错: %v", err)
	}
	if _, err := s.SearchEmails(ctx, "c", "foo* AND bar", 20); err != nil {
		t.Fatalf("含通配符的查询不应报错: %v", err)
	}
}

// 删除邮件后索引必须同步清理, 否则搜索会返回已不存在的行。
func TestSearchIndexFollowsDeletes(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForSearch(t, s)

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return DeleteEmails(ctx, tx, "c", []string{"e1"})
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := s.SearchEmails(ctx, "c", "发票申请", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("已删除的邮件仍被搜到: %v", ids(got))
	}
}

func ids(list []EmailSummary) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}
