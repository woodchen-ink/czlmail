package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestTemplateCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	tpl := Template{
		ID: "t1", Name: "报价回复", Category: "销售",
		Subject:    "关于 {{company}} 的报价",
		Body:       "{{recipient_name}}你好",
		DefaultTo:  []string{"sales@example.net"},
		IsFavorite: true,
	}

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return SaveTemplate(ctx, tx, tpl)
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Template(ctx, "t1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Name != "报价回复" || len(got.DefaultTo) != 1 || !got.IsFavorite {
		t.Fatalf("回读不一致: %+v", got)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Error("时间戳未填充")
	}
}

// 删除必须留墓碑: 物理删除会让模板在下次同步时从别的设备原样长回来。
func TestDeleteTemplateLeavesTombstone(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return SaveTemplate(ctx, tx, Template{ID: "t1", Name: "x", Body: "hello"})
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return DeleteTemplate(ctx, tx, "t1")
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list, err := s.Templates(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("已删除模板不该出现在列表里: %+v", list)
	}

	var rows, deleted int
	s.DB().QueryRow(`SELECT COUNT(*), COUNT(deleted_at) FROM email_templates WHERE id='t1'`).Scan(&rows, &deleted)
	if rows != 1 || deleted != 1 {
		t.Errorf("墓碑缺失: rows=%d deleted=%d", rows, deleted)
	}

	// 墓碑不该留着正文 —— 它可能含有敏感内容, 且同步时白白占带宽。
	var body string
	s.DB().QueryRow(`SELECT body FROM email_templates WHERE id='t1'`).Scan(&body)
	if body != "" {
		t.Errorf("墓碑仍保留正文: %q", body)
	}
}

// 重新保存一个已删除的 id 等于恢复它。
func TestSaveTemplateRevivesTombstone(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	save := func(name string) {
		if err := s.WithTx(ctx, func(tx *sql.Tx) error {
			return SaveTemplate(ctx, tx, Template{ID: "t1", Name: name})
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	save("原始")
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return DeleteTemplate(ctx, tx, "t1")
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	save("恢复")

	list, err := s.Templates(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "恢复" {
		t.Errorf("恢复失败: %+v", list)
	}
}

func TestPurgeDeletedTemplates(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := SaveTemplate(ctx, tx, Template{ID: "t1", Name: "x"}); err != nil {
			return err
		}
		return DeleteTemplate(ctx, tx, "t1")
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return PurgeDeletedTemplates(ctx, tx, time.Now().Add(time.Hour))
	}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM email_templates`).Scan(&n)
	if n != 0 {
		t.Errorf("墓碑未被清理, 剩余 %d", n)
	}
}
