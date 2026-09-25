package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 工具清单: 名称齐全、参数 schema 能生成(嵌入的 emailBatchIn 要展开到顶层)、每个工具都带风险注解。
func TestMCPToolList(t *testing.T) {
	ctx := context.Background()
	a := &App{ctx: ctx}
	ct, st := mcp.NewInMemoryTransports()
	if _, err := a.newMCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools := map[string]*mcp.Tool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	want := []string{
		"list_accounts", "list_mailboxes", "list_emails", "search_emails", "read_email", "get_thread", "read_attachment", "save_attachment", "list_identities",
		"mark_read", "flag_emails", "set_label", "move_emails", "create_draft", "send_email",
		"list_calendars", "list_events", "get_event", "create_event", "update_event", "delete_event", "respond_event",
		"list_tasks", "create_task", "complete_task", "update_task", "delete_task",
		"search_contacts", "get_contact", "contact_history", "list_files", "read_file",
	}
	for _, name := range want {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("missing tool %s", name)
			continue
		}
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", name)
		}
	}
	if len(tools) != len(want) {
		t.Errorf("got %d tools, want %d", len(tools), len(want))
	}

	schema, _ := json.Marshal(tools["move_emails"].InputSchema)
	for _, field := range []string{`"emailIds"`, `"accountId"`, `"to"`} {
		if !strings.Contains(string(schema), field) {
			t.Errorf("move_emails schema lacks %s: %s", field, schema)
		}
	}
	if !tools["list_emails"].Annotations.ReadOnlyHint || tools["move_emails"].Annotations.ReadOnlyHint {
		t.Error("read-only hints are wrong")
	}
	if ow := tools["send_email"].Annotations.OpenWorldHint; ow == nil || !*ow {
		t.Error("send_email must be marked open-world")
	}
}

func TestWithSubjectPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"Hello":        "Re: Hello",
		"RE: Hello":    "RE: Hello",
		"re : x":       "re : x",
		"Fwd: invoice": "Re: Fwd: invoice",
	} {
		if got := withSubjectPrefix("Re:", in); got != want {
			t.Errorf("Re %q = %q, want %q", in, got, want)
		}
	}
	if got := withSubjectPrefix("Fwd:", "转发：通知"); got != "转发：通知" {
		t.Errorf("Fwd = %q", got)
	}
}

func TestTextToHTML(t *testing.T) {
	got := textToHTML("a<b>\nline2\n\n\npara2")
	if want := "<p>a&lt;b&gt;<br>line2</p><p>para2</p>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMatchLocal(t *testing.T) {
	day := time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)
	e := store.EmailSummary{
		Subject: "Invoice 42", From: []store.Address{{Name: "DHL Express", Email: "noreply@dhl.com"}},
		ReceivedAt: day, IsUnread: true, MailboxIDs: []string{"inbox"},
	}
	cases := []struct {
		f    syncer.EmailFilter
		want bool
	}{
		{syncer.EmailFilter{From: "dhl"}, true},
		{syncer.EmailFilter{From: "fedex"}, false},
		{syncer.EmailFilter{Subject: "invoice"}, true},
		{syncer.EmailFilter{MailboxID: "archive"}, false},
		{syncer.EmailFilter{After: day.Add(-time.Hour), Before: day.Add(time.Hour)}, true},
		{syncer.EmailFilter{Before: day}, false},
		{syncer.EmailFilter{Unread: true}, true},
		{syncer.EmailFilter{Flagged: true}, false},
	}
	for i, c := range cases {
		if got := matchLocal(c.f, e); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}

func TestIsTextual(t *testing.T) {
	for _, c := range []struct {
		name, ct string
		want     bool
	}{
		{"a.txt", "text/plain; charset=utf-8", true},
		{"data.csv", "application/octet-stream", true},
		{"x.json", "application/json", true},
		{"r.pdf", "application/pdf", false},
		{"img.png", "image/png", false},
		{"noext", "application/octet-stream", false},
	} {
		if got := isTextual(c.name, c.ct); got != c.want {
			t.Errorf("%s %s = %v", c.name, c.ct, got)
		}
	}
}

func TestOccurrenceTimes(t *testing.T) {
	d := &EventDetail{Start: "2026-09-01T09:00:00", End: "2026-09-01T10:30:00"}
	start, end, err := occurrenceTimes(d, "2026-09-08T09:00:00")
	if err != nil || start != "2026-09-08T09:00:00" || end != "2026-09-08T10:30:00" {
		t.Errorf("got %s %s %v", start, end, err)
	}
}

func TestTruncateText(t *testing.T) {
	s := strings.Repeat("中", 10) // 30 字节
	got := truncateText(s, 10)
	if !strings.HasPrefix(got, "中中中\n") {
		t.Errorf("cut inside a rune: %q", got)
	}
}
