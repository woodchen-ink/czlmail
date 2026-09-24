package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/mcpbridge"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 本地 MCP 端点: 无令牌被拒, 带令牌能列工具并调用读工具; 未开启发信时 send_email 被拒。
func TestIntegrationMCP(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" || user == "" || pass == "" {
		t.Skip("set CZLMAIL_TEST_HOST / _USER / _PASS to run")
	}
	// 连接信息写到临时配置目录, 不覆盖本机真实的 mcp.json。
	t.Setenv("AppData", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	endpoint, _ := auth.SessionEndpoint(host)
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	for _, acc := range a.sync.Accounts() {
		if err := a.sync.SyncAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.persistAccounts(ctx, client, st); err != nil {
		t.Fatal(err)
	}

	status, err := a.SetMCP(true, false, false)
	if err != nil || !status.Running {
		t.Fatalf("start: %+v %v", status, err)
	}
	defer a.stopMCP()

	resp, err := http.Post(status.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request without token: status %d", resp.StatusCode)
	}

	info, ok := mcpbridge.ReadInfo(ctx, mustInfoPath(t))
	if !ok || info.Token != status.Token {
		t.Fatalf("mcp.json not readable: %+v", info)
	}

	mc := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: status.URL, HTTPClient: &http.Client{Transport: mcpbridge.Bearer{Token: status.Token}}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	t.Logf("tools: %v", names)

	call := func(name string, args any) *mcp.CallToolResult {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	text := func(r *mcp.CallToolResult) string {
		b, _ := json.Marshal(r.StructuredContent)
		if len(b) > 300 {
			b = b[:300]
		}
		return string(b)
	}

	for _, c := range []struct {
		name string
		args any
	}{
		{"list_accounts", map[string]any{}},
		{"list_mailboxes", map[string]any{}},
		{"list_emails", map[string]any{"limit": 3}},
		{"search_emails", map[string]any{"query": "通知", "limit": 3}},
		{"search_emails", map[string]any{"mailbox": "inbox", "after": "2026-01-01", "unread": true, "limit": 3}},
		{"list_identities", map[string]any{}},
		{"list_calendars", map[string]any{}},
		{"list_tasks", map[string]any{}},
		{"list_events", map[string]any{"from": "2026-09-01T00:00:00+08:00", "to": "2026-10-01T00:00:00+08:00"}},
		{"search_contacts", map[string]any{"query": "qq.com"}},
		{"list_files", map[string]any{}},
	} {
		r := call(c.name, c.args)
		if r.IsError {
			t.Errorf("%s returned error: %+v", c.name, r.Content)
			continue
		}
		t.Logf("%s → %s", c.name, text(r))
	}

	r := call("send_email", map[string]any{"to": []string{user}, "subject": "x", "body": "x"})
	if !r.IsError {
		t.Fatal("send_email must be rejected while sending is disabled")
	}
	r = call("move_emails", map[string]any{"emailIds": []string{"x"}, "to": "trash"})
	if !r.IsError {
		t.Fatal("move_emails must be rejected while modifying is disabled")
	}

	// 回复收件箱第一封: 草稿要挂进原会话(In-Reply-To)并带上 Re: 主题, 测完删掉。
	var listed struct {
		Emails []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"emails"`
	}
	b, _ := json.Marshal(call("list_emails", map[string]any{"limit": 1}).StructuredContent)
	_ = json.Unmarshal(b, &listed)
	if len(listed.Emails) == 0 {
		return
	}
	orig := listed.Emails[0]
	r = call("get_thread", map[string]any{"emailId": orig.ID, "includeBodies": true})
	if r.IsError {
		t.Fatalf("get_thread: %+v", r.Content)
	}
	r = call("create_draft", map[string]any{"mode": "reply", "emailId": orig.ID, "body": "czlmail integration test"})
	if r.IsError {
		t.Fatalf("create_draft: %+v", r.Content)
	}
	var draft struct {
		DraftID   string `json:"draftId"`
		AccountID string `json:"accountId"`
		Subject   string `json:"subject"`
	}
	b, _ = json.Marshal(r.StructuredContent)
	_ = json.Unmarshal(b, &draft)
	defer a.DiscardDraft(draft.AccountID, draft.DraftID)
	if !strings.HasPrefix(strings.ToLower(draft.Subject), "re") {
		t.Errorf("reply subject = %q (original %q)", draft.Subject, orig.Subject)
	}
	if err := a.sync.SyncMail(ctx, draft.AccountID); err != nil {
		t.Fatal(err)
	}
	d, err := a.GetEmail(draft.AccountID, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := a.GetEmail(draft.AccountID, orig.ID)
	if src.MessageID != "" && d.InReplyTo != src.MessageID {
		t.Errorf("draft In-Reply-To = %q, want %q", d.InReplyTo, src.MessageID)
	}
	if len(d.From) == 0 {
		t.Error("draft has no From header")
	}
}

func mustInfoPath(t *testing.T) string {
	p, err := mcpbridge.InfoPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
