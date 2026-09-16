package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 邮件模板。本地存储, 服务器文件存储作为跨设备同步的真相(见 CLAUDE.md)。

func (a *App) ListTemplates() ([]store.Template, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Templates(a.ctx)
}

// SaveTemplate 新建或更新模板。ID 为空时自动生成。
func (a *App) SaveTemplate(t store.Template) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	if t.Name == "" {
		return "", fmt.Errorf("2040 template name is required")
	}
	if t.ID == "" {
		t.ID, err = newID()
		if err != nil {
			return "", err
		}
	}

	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		return store.SaveTemplate(a.ctx, tx, t)
	}); err != nil {
		return "", err
	}
	a.syncTemplatesSoon()
	return t.ID, nil
}

func (a *App) DeleteTemplate(id string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		return store.DeleteTemplate(a.ctx, tx, id)
	}); err != nil {
		return err
	}
	a.syncTemplatesSoon()
	return nil
}

// TemplateFill 是套用模板后的结果, 直接喂给撰写窗口。
type TemplateFill struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	IsHTML  bool     `json:"isHtml"`
	To      []string `json:"to"`
	CC      []string `json:"cc"`
	BCC     []string `json:"bcc"`
	// Unresolved 是仍未填充的占位符, 界面据此提示用户补齐。
	Unresolved []string `json:"unresolved"`
}

// ApplyTemplate 套用模板, 填充内置占位符与用户提供的值。
func (a *App) ApplyTemplate(id string, values map[string]string, recipientName string) (*TemplateFill, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}

	t, err := st.Template(a.ctx, id)
	if err != nil {
		return nil, err
	}

	ctx := store.PlaceholderContext{
		SenderName:    a.displayName(),
		RecipientName: recipientName,
		Now:           time.Now(),
	}

	subject := store.ApplyPlaceholders(t.Subject, values, ctx)
	body := store.ApplyPlaceholders(t.Body, values, ctx)

	return &TemplateFill{
		Subject:    subject,
		Body:       body,
		IsHTML:     t.IsHTML,
		To:         t.DefaultTo,
		CC:         t.DefaultCC,
		BCC:        t.DefaultBCC,
		Unresolved: store.ExtractPlaceholders(subject + "\n" + body),
	}, nil
}

// TemplatePlaceholders 返回模板中的占位符, 以及哪些是自动填充的。
func (a *App) TemplatePlaceholders(id string) (map[string][]string, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	t, err := st.Template(a.ctx, id)
	if err != nil {
		return nil, err
	}

	all := store.ExtractPlaceholders(t.Subject + "\n" + t.Body)
	builtin := map[string]bool{}
	for _, b := range store.BuiltinPlaceholders {
		builtin[b] = true
	}

	auto := []string{}
	manual := []string{}
	for _, name := range all {
		if builtin[name] {
			auto = append(auto, name)
		} else {
			manual = append(manual, name)
		}
	}
	return map[string][]string{"auto": auto, "manual": manual}, nil
}

func (a *App) displayName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Username
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("2041 generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
