package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 通讯录与网盘的只读工具。

func (a *App) addContactFileTools(server *mcp.Server) {
	type contactsIn struct {
		Query string `json:"query" jsonschema:"姓名、邮箱、电话或公司"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_contacts",
		Description: "搜索通讯录联系人, 同时查同一邮局的成员目录(directory, 如同事)。",
		Annotations: readOnlyTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contactsIn) (*mcp.CallToolResult, any, error) {
		list, err := a.ListContacts("", "", in.Query)
		if err != nil {
			return nil, nil, err
		}
		if len(list) > 50 {
			list = list[:50]
		}
		// 目录只是补充, 服务器不支持或查询失败时照样返回通讯录结果。
		dir, _ := a.SearchDirectory(in.Query)
		if len(dir) > 20 {
			dir = dir[:20]
		}
		return nil, map[string]any{"contacts": list, "directory": dir}, nil
	})

	type contactIn struct {
		AccountID string `json:"accountId"`
		ContactID string `json:"contactId"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_contact", Description: "读取联系人详情: 全部邮箱、电话、地址、公司职位、备注等。", Annotations: readOnlyTool()},
		func(ctx context.Context, _ *mcp.CallToolRequest, in contactIn) (*mcp.CallToolResult, any, error) {
			c, err := a.GetContact(in.AccountID, in.ContactID)
			if err != nil {
				return nil, nil, err
			}
			// 照片是 data URI, 动辄几十 KB, 对模型没有用。
			c.Photo = ""
			return nil, c, nil
		})

	type historyIn struct {
		Email string `json:"email" jsonschema:"对方邮箱地址"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "contact_history",
		Description: "与某人的往来: 最近的邮件(对方发来或发给对方)与接下来有对方参加的日程。",
		Annotations: readOnlyTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in historyIn) (*mcp.CallToolResult, any, error) {
		addr := strings.TrimSpace(in.Email)
		if !strings.Contains(addr, "@") {
			return nil, nil, errors.New("email must be an address; use search_contacts to find it")
		}
		emails, err := a.RecentEmailsWith(addr)
		if err != nil {
			return nil, nil, err
		}
		out := make([]map[string]any, 0, len(emails))
		for _, e := range emails {
			out = append(out, map[string]any{
				"accountId": e.AccountID, "id": e.ID, "threadId": e.ThreadID, "subject": e.Subject, "from": e.From,
				"receivedAt": e.ReceivedAt, "preview": e.Preview, "isUnread": e.IsUnread,
			})
		}
		events, _ := a.UpcomingEventsWith(addr)
		return nil, map[string]any{"emails": out, "upcomingEvents": events}, nil
	})

	type filesIn struct {
		AccountID string `json:"accountId,omitempty" jsonschema:"省略时为个人账号"`
		FolderID  string `json:"folderId,omitempty" jsonschema:"省略时为根目录"`
		Query     string `json:"query,omitempty" jsonschema:"按文件名搜索整个网盘; 给了就忽略 folderId"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "list_files", Description: "列出网盘目录内容, 或按文件名搜索整个网盘。", Annotations: readOnlyTool()},
		func(ctx context.Context, _ *mcp.CallToolRequest, in filesIn) (*mcp.CallToolResult, any, error) {
			acc, err := a.mcpAccount(in.AccountID)
			if err != nil {
				return nil, nil, err
			}
			var out []map[string]any
			if q := strings.TrimSpace(in.Query); q != "" {
				list, err := a.SearchFiles(acc, q)
				if err != nil {
					return nil, nil, err
				}
				if len(list) > 100 {
					list = list[:100]
				}
				for _, n := range list {
					out = append(out, fileEntry(n.ID, n.ParentID, n.Name, n.IsDirectory, n.ContentType, n.Size, n.Modified))
				}
				return nil, map[string]any{"accountId": acc, "files": out}, nil
			}
			list, err := a.ListFiles(acc, in.FolderID)
			if err != nil {
				return nil, nil, err
			}
			for _, n := range list {
				out = append(out, fileEntry(n.ID, n.ParentID, n.Name, n.IsDirectory, n.ContentType, n.Size, n.Modified))
			}
			return nil, map[string]any{"accountId": acc, "files": out}, nil
		})

	type readFileIn struct {
		AccountID string `json:"accountId,omitempty" jsonschema:"省略时为个人账号"`
		FileID    string `json:"fileId"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: "读取网盘里文本类文件(txt/md/csv/json/xml/html 等)的内容。PDF、图片、Office 文档不支持。",
		Annotations: readOnlyTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readFileIn) (*mcp.CallToolResult, any, error) {
		acc, err := a.mcpAccount(in.AccountID)
		if err != nil {
			return nil, nil, err
		}
		st, err := a.currentStore()
		if err != nil {
			return nil, nil, err
		}
		n, err := st.FileNodeByID(a.ctx, acc, in.FileID)
		if err != nil {
			return nil, nil, fmt.Errorf("file %q not found", in.FileID)
		}
		if n.IsDirectory || n.BlobID == "" {
			return nil, nil, errors.New("this is a folder; use list_files")
		}
		text, err := a.readTextBlob(ctx, acc, n.BlobID, n.Name, n.ContentType)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"name": n.Name, "content": text}, nil
	})
}

func fileEntry(id, parent, name string, dir bool, contentType string, size, modified int64) map[string]any {
	m := map[string]any{"id": id, "parentId": parent, "name": name, "isDirectory": dir}
	if !dir {
		m["type"], m["size"] = contentType, size
		m["readable"] = isTextual(name, contentType)
	}
	if modified > 0 {
		m["modified"] = time.Unix(modified, 0).Format(time.RFC3339)
	}
	return m
}
