package notes

import (
	"testing"
	"time"
)

// TestFormatDateTokens matches the Obsidian daily-note layout used before migrating.
func TestFormatDateTokens(t *testing.T) {
	day := time.Date(2025, 3, 21, 9, 5, 7, 0, time.Local)
	cases := map[string]string{
		"dailynote/YYYY/MM":    "dailynote/2025/03",
		"YYYY-MM-DD-dddd":      "2025-03-21-星期五",
		"YY.M.D ddd HH:mm:ss":  "25.3.21 周五 09:05:07",
		"[Week of] YYYY-MM-DD": "Week of 2025-03-21",
		"日记 YYYY年M月D日":         "日记 2025年3月21日",
	}
	for layout, want := range cases {
		if got := FormatDate(layout, day, "zh"); got != want {
			t.Errorf("%q: got %q want %q", layout, got, want)
		}
	}
	if got := FormatDate("dddd ddd", day, "en"); got != "Friday Fri" {
		t.Errorf("english weekday: %q", got)
	}
	got := ExpandTemplate("# {{title}}\ncreated: {{date}} {{time}}\n{{date:YYYY/MM}} {{ other }}", day, "2025-03-21-星期五", "zh")
	if got != "# 2025-03-21-星期五\ncreated: 2025-03-21 09:05\n2025/03 {{ other }}" {
		t.Errorf("template: %q", got)
	}
}

// TestEnsureNoteCreatesOnceAndReadsTemplates covers the one-click diary path.
func TestEnsureNoteCreatesOnceAndReadsTemplates(t *testing.T) {
	s := testService(t, newRemote())
	vault := vaultID(t, s)
	first, created, err := s.EnsureNote(vault, []string{"dailynote", "2025", "03"}, "2025-03-21-星期五", "# 今日主题")
	if err != nil || !created || first.Path != "dailynote/2025/03" || first.Created == 0 {
		t.Fatalf("first: %v %v %+v", err, created, first)
	}
	second, created, err := s.EnsureNote(vault, []string{"dailynote", "2025", "03"}, "2025-03-21-星期五.md", "other")
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second: %v %v %s", err, created, second.ID)
	}
	if read, _ := s.Read(vault, first.ID); read.Content != "# 今日主题" {
		t.Fatalf("existing diary overwritten: %q", read.Content)
	}
	if _, err = s.Create(Edit{VaultID: vault, Name: "模板"}, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.EnsureNote(vault, []string{"模板"}, "日记", "{{date}}"); err != nil {
		t.Fatal(err)
	}
	tpl, err := s.ReadPath(vault, "模板/日记.md")
	if err != nil || tpl.Content != "{{date}}" {
		t.Fatalf("template: %v %q", err, tpl.Content)
	}
	if _, err = s.ReadPath(vault, "模板/missing.md"); err != ErrNotFound {
		t.Fatalf("missing template: %v", err)
	}
}
