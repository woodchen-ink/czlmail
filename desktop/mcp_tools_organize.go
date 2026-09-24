package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 整理邮件的工具。已读、星标、标签不丢数据, 直接可用;
// 移动(含归档、移到回收站、标为垃圾邮件)会让邮件离开用户视线, 需开「允许 AI 整理邮件与日程」。
// 不提供彻底删除与清空文件夹: 回收站里的邮件还能找回, 这是提示注入时的最后一道保险。

// mcpMaxBatch 限制一次操作的邮件数, 避免一次调用改动整个邮箱。
const mcpMaxBatch = 200

type emailBatchIn struct {
	AccountID string   `json:"accountId,omitempty" jsonschema:"省略时为个人账号"`
	EmailIDs  []string `json:"emailIds" jsonschema:"邮件 id, 一次最多 200 封"`
}

func (a *App) mcpBatch(in emailBatchIn) (string, error) {
	if len(in.EmailIDs) == 0 {
		return "", errors.New("emailIds is empty")
	}
	if len(in.EmailIDs) > mcpMaxBatch {
		return "", fmt.Errorf("at most %d emails per call", mcpMaxBatch)
	}
	return a.mcpAccount(in.AccountID)
}

func (a *App) addOrganizeTools(server *mcp.Server) {
	type markIn struct {
		emailBatchIn
		Read bool `json:"read" jsonschema:"true 标为已读, false 标为未读"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "mark_read", Description: "标记邮件为已读或未读。", Annotations: writeTool(false, true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in markIn) (*mcp.CallToolResult, any, error) {
			acc, err := a.mcpBatch(in.emailBatchIn)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"ok": true}, a.MarkRead(acc, in.EmailIDs, in.Read)
		})

	type flagIn struct {
		emailBatchIn
		Flagged bool `json:"flagged" jsonschema:"true 加星标, false 取消"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "flag_emails", Description: "给邮件加上或取消星标。", Annotations: writeTool(false, true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in flagIn) (*mcp.CallToolResult, any, error) {
			acc, err := a.mcpBatch(in.emailBatchIn)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"ok": true}, a.MarkFlagged(acc, in.EmailIDs, in.Flagged)
		})

	type labelIn struct {
		emailBatchIn
		Label  string `json:"label" jsonschema:"标签名, 只能用可见 ASCII 字符(服务器不接受中文标签); 已有标签见 list_mailboxes"`
		Remove bool   `json:"remove,omitempty" jsonschema:"true 时去掉该标签"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "set_label", Description: "给邮件添加或去掉标签。", Annotations: writeTool(false, true)},
		func(ctx context.Context, _ *mcp.CallToolRequest, in labelIn) (*mcp.CallToolResult, any, error) {
			acc, err := a.mcpBatch(in.emailBatchIn)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"ok": true}, a.SetLabel(acc, in.EmailIDs, in.Label, !in.Remove)
		})

	type moveIn struct {
		emailBatchIn
		To string `json:"to" jsonschema:"目标: archive(归档)、trash(回收站)、junk(垃圾邮件), 或文件夹 id/名称/类别"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "move_emails",
		Description: "移动邮件: 归档、移到回收站、标为垃圾邮件或移到指定文件夹。需要用户开启「允许 AI 整理邮件与日程」。不会彻底删除。",
		Annotations: writeTool(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in moveIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowModify); err != nil {
			return nil, nil, err
		}
		acc, err := a.mcpBatch(in.emailBatchIn)
		if err != nil {
			return nil, nil, err
		}
		switch in.To {
		case "archive":
			err = a.ArchiveEmails(acc, in.EmailIDs)
		case "trash":
			err = a.TrashEmails(acc, in.EmailIDs)
		case "junk":
			// 与界面相同, 同时打 $junk 供服务端学习。
			err = a.MarkJunk(acc, in.EmailIDs, true)
		default:
			box, berr := a.mcpMailbox(acc, in.To)
			if berr != nil {
				return nil, nil, berr
			}
			err = a.MoveEmails(acc, in.EmailIDs, box)
		}
		return nil, map[string]any{"ok": err == nil, "moved": len(in.EmailIDs)}, err
	})
}
