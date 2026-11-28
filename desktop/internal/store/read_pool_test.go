package store

import (
	"context"
	"testing"
	"time"
)

// 同步在写连接上开着事务时, 界面的查询走只读连接, 不能排在事务后面。
func TestReadsDoNotWaitForWriteTransaction(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.SetStringSetting(ctx, "k", "before"); err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET value = 'during' WHERE key = 'k'`); err != nil {
		t.Fatal(err)
	}

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, err := s.StringSetting(rctx, "k")
	if err != nil {
		t.Fatalf("read during write transaction: %v", err)
	}
	// 读到的是事务开始前已提交的值。
	if got != "before" {
		t.Fatalf("got %q, want the committed value", got)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StringSetting(ctx, "k"); got != "during" {
		t.Fatalf("after commit got %q", got)
	}
}

// 写语句误走只读连接时报错, 而不是绕过写连接的排队。
func TestReadPoolRejectsWrites(t *testing.T) {
	s := openTemp(t)
	if _, err := s.rd.ExecContext(context.Background(), `INSERT INTO app_settings (key, value) VALUES ('x', 'y')`); err == nil {
		t.Fatal("write on the read pool succeeded")
	}
}
