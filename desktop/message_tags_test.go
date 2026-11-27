package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestTagKeyword(t *testing.T) {
	cases := map[string]string{
		"Účetnictví 2026":   "ucetnictvi-2026",
		"  Ke  zpracování ": "ke-zpracovani",
		"Žluťoučký kůň":     "zlutoucky-kun",
		"R&D / Łódź":        "r-d-lodz",
		"Straße":            "strasse",
		"项目":                "tag",
		"Junk":              "tag-junk",
		"a(b)c%d*e":         "abcde",
	}
	for in, want := range cases {
		got := tagKeyword(in)
		if got != want {
			t.Errorf("tagKeyword(%q) = %q, want %q", in, got, want)
		}
		if err := validateLabel(got); err != nil {
			t.Errorf("tagKeyword(%q) = %q is not a valid keyword: %v", in, got, err)
		}
	}
}

// 新标签的关键字由名字生成, 撞上别的标签时加序号; 改名不改关键字; 删除留墓碑。
func TestSaveMessageTag(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{ctx: ctx, store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.tap = func(string, ...any) {}

	work, err := a.SaveMessageTag(MessageTag{Name: " Práce ", Color: "blue"})
	if err != nil {
		t.Fatal(err)
	}
	if work != (MessageTag{Keyword: "prace", Name: "Práce", Color: "blue"}) {
		t.Fatalf("new tag = %+v", work)
	}
	// 同名 (不分大小写) 是同一个标签。
	if again, _ := a.SaveMessageTag(MessageTag{Name: "práce", Color: "green"}); again.Keyword != "prace" {
		t.Fatalf("same name got keyword %q", again.Keyword)
	}
	// 名字不同但生成同一个关键字: 加序号。
	other, err := a.SaveMessageTag(MessageTag{Name: "Prace", Color: "chartreuse"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Keyword != "prace-2" || other.Color != "" {
		t.Fatalf("clashing tag = %+v", other)
	}
	// 改名: 关键字不变。
	if _, err := a.SaveMessageTag(MessageTag{Keyword: "prace-2", Name: "Úřad", Color: "red"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveMessageTag(MessageTag{Keyword: "$flagged", Name: "x"}); err == nil {
		t.Fatal("system keywords must be rejected")
	}

	tags, err := a.GetMessageTags()
	if err != nil {
		t.Fatal(err)
	}
	want := []MessageTag{{Keyword: "prace", Name: "práce", Color: "green"}, {Keyword: "prace-2", Name: "Úřad", Color: "red"}}
	if !slices.Equal(tags, want) {
		t.Fatalf("tags = %+v, want %+v", tags, want)
	}

	if err := a.DeleteMessageTag("PRACE"); err != nil {
		t.Fatal(err)
	}
	tags, _ = a.GetMessageTags()
	if len(tags) != 1 || tags[0].Keyword != "prace-2" {
		t.Fatalf("after delete tags = %+v", tags)
	}
	rows, _ := st.TimedSettingsWithPrefix(ctx, messageTagPrefix)
	if r, ok := rows["prace"]; !ok || r.Value != "" {
		t.Fatalf("deleted tag must leave a tombstone, rows = %+v", rows)
	}
}

// 标签与待办旗标写到服务器的关键字上, 列表摘要带出标签 (不含系统标志与别的客户端的垃圾邮件标记)。
func TestTaskFlagAndLabelsOnServer(t *testing.T) {
	srv := jmaptest.New(t)
	u := srv.AddUser("me", "pw")
	acc := srv.AddAccount("a", "me@example.test", jmaptest.Mail, jmaptest.Submission)
	u.Grant(acc, true)
	id := acc.AddEmail(jmaptest.Email{Subject: "task", From: "boss@example.test"})
	acc.SetKeyword(id, "nonjunk", true) // Thunderbird 的垃圾邮件判定
	acc.SetKeyword(id, "$label1", true) // Thunderbird 的旧标签 (系统标志, 不显示)

	ta := newTestApp(t)
	ta.signInPrimary(srv, "me", "pw")
	ta.waitSubjects("a", "inbox", "task")
	a := ta.a

	summary := func() store.EmailSummary {
		t.Helper()
		list, err := ta.store().EmailsByMailbox(ta.ctx, "a", "inbox", 10, 0)
		if err != nil || len(list) != 1 {
			t.Fatalf("list = %+v, %v", list, err)
		}
		return list[0]
	}
	keywords := func() []string {
		info, _ := acc.EmailByID(id)
		return slices.DeleteFunc(info.Keywords, func(k string) bool { return k == "nonjunk" || k == "$label1" })
	}

	if s := summary(); len(s.Labels) != 0 || s.IsFlagged || s.IsCompleted {
		t.Fatalf("initial summary = %+v", s)
	}

	if err := a.SetLabel("a", []string{id}, "Prace", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLabel("a", []string{id}, "urgent", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetTaskFlag("a", []string{id}, "flagged"); err != nil {
		t.Fatal(err)
	}
	if got := keywords(); !slices.Equal(got, []string{"$flagged", "prace", "urgent"}) {
		t.Fatalf("server keywords = %v", got)
	}
	s := summary()
	if !slices.Equal(s.Labels, []string{"prace", "urgent"}) || !s.IsFlagged || s.IsCompleted {
		t.Fatalf("flagged summary = %+v", s)
	}
	d, err := a.GetEmail("a", id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Labels, []string{"prace", "urgent"}) {
		t.Fatalf("detail labels = %v", d.Labels)
	}

	if err := a.SetTaskFlag("a", []string{id}, "completed"); err != nil {
		t.Fatal(err)
	}
	if got := keywords(); !slices.Equal(got, []string{"$completed", "prace", "urgent"}) {
		t.Fatalf("completed keywords = %v", got)
	}
	if s := summary(); s.IsFlagged || !s.IsCompleted {
		t.Fatalf("completed summary = %+v", s)
	}

	if err := a.SetTaskFlag("a", []string{id}, "none"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLabel("a", []string{id}, "urgent", false); err != nil {
		t.Fatal(err)
	}
	if got := keywords(); !slices.Equal(got, []string{"prace"}) {
		t.Fatalf("cleared keywords = %v", got)
	}
	if err := a.SetTaskFlag("a", []string{id}, "done"); err == nil {
		t.Fatal("unknown state must be rejected")
	}

	labels, err := a.ListLabels("a")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(labels, []string{"prace"}) {
		t.Fatalf("ListLabels = %v", labels)
	}
}
