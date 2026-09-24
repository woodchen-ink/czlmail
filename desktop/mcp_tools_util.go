package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/pim"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// MCP 工具的公共部分: 风险注解、权限开关、账号与文件夹解析、输出整形。

// 工具注解供 AI 客户端决定哪些调用要先征求用户同意。
// 除发信与回复邀请外, 所有工具只作用于用户自己的数据, OpenWorldHint 为 false。
func readOnlyTool() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}
}

// writeTool 标记写工具。destructive 表示会删除或覆盖已有数据; idempotent 表示重复调用结果相同。
func writeTool(destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(destructive), IdempotentHint: idempotent, OpenWorldHint: new(false)}
}

// outwardTool 标记会向他人发出消息的工具(发信、回复邀请)。
func outwardTool() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)}
}

// mcpRequire 检查用户是否在 AI 助手页开了对应权限。
// 错误信息写给模型看, 让它转告用户去哪里开, 而不是反复重试。
func (a *App) mcpRequire(setting string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if on, _ := st.BoolSetting(a.ctx, setting, false); on {
		return nil
	}
	switch setting {
	case store.SettingMCPAllowSend:
		return errors.New("sending is disabled: the user must turn on 「允许 AI 发送邮件」 in CZL Mail → AI 助手. Save a draft with create_draft instead")
	default:
		return errors.New("this change is disabled: the user must turn on 「允许 AI 整理邮件与日程」 in CZL Mail → AI 助手")
	}
}

func (a *App) mcpAccount(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	if acc := a.personalAccountID(st); acc != "" {
		return acc, nil
	}
	return "", errors.New("no account available")
}

// mcpAccounts 返回要遍历的账号: 指定了就只有它, 否则全部账号。
func (a *App) mcpAccounts(id string) ([]string, error) {
	if id != "" {
		return []string{id}, nil
	}
	accs, err := a.ListAccounts()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(accs))
	for _, acc := range accs {
		out = append(out, acc.ID)
	}
	return out, nil
}

// mcpMailbox 把文件夹 id、类别(inbox/archive…)或名称解析成 id, 空值为收件箱。
func (a *App) mcpMailbox(accountID, mailbox string) (string, error) {
	boxes, err := a.ListMailboxes(accountID)
	if err != nil {
		return "", err
	}
	if mailbox == "" {
		mailbox = "inbox"
	}
	for _, b := range boxes {
		if b.ID == mailbox || b.Kind == mailbox || strings.EqualFold(b.Name, mailbox) {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("mailbox %q not found", mailbox)
}

// mailboxNames 是 文件夹 id → 名称, 让模型看到邮件在哪个文件夹而不是一串 id。
func (a *App) mailboxNames(accountID string) map[string]string {
	boxes, err := a.ListMailboxes(accountID)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(boxes))
	for _, b := range boxes {
		out[b.ID] = b.Name
	}
	return out
}

// summarize 把邮件摘要整形成给模型看的字段。names 非空时附上所在文件夹名。
func summarize(list []store.EmailSummary, names map[string]string) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		m := map[string]any{
			"id": e.ID, "threadId": e.ThreadID, "subject": e.Subject, "from": e.From, "receivedAt": e.ReceivedAt,
			"preview": e.Preview, "isUnread": e.IsUnread, "isFlagged": e.IsFlagged, "hasAttachment": e.HasAttachment,
		}
		if e.ThreadCount > 1 {
			m["threadCount"] = e.ThreadCount
		}
		if len(names) > 0 && len(e.MailboxIDs) > 0 {
			folders := make([]string, 0, len(e.MailboxIDs))
			for _, id := range e.MailboxIDs {
				if n, ok := names[id]; ok {
					folders = append(folders, n)
				}
			}
			m["folders"] = folders
		}
		out = append(out, m)
	}
	return out
}

func parseAddressList(list []string) []store.Address {
	out := make([]store.Address, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i := strings.LastIndex(s, "<"); i >= 0 && strings.HasSuffix(s, ">") {
			out = append(out, store.Address{Name: strings.TrimSpace(strings.Trim(s[:i], `" `)), Email: s[i+1 : len(s)-1]})
			continue
		}
		out = append(out, store.Address{Email: s})
	}
	return out
}

// parseMCPTime 接受 RFC 3339、本机时间 YYYY-MM-DDTHH:MM:SS 与日期 YYYY-MM-DD(当天零点)。
func parseMCPTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(pim.LocalDateTime, s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: use RFC 3339 or YYYY-MM-DD", s)
}

// truncateText 按字节截断且不切断多字节字符。
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n…(已截断)"
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
