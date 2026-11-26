package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// TestJournalDefaultsFollowLanguage: the default journal folder and template are in the UI language, English otherwise.
func TestJournalDefaultsFollowLanguage(t *testing.T) {
	for lang, want := range map[string]string{"zh-CN": "日记", "en": "Journal", "cs": "Deník", "ru": "Дневник", "fr": "Journal", "": "Journal"} {
		if got := localized(journalFolderNames, lang); got != want {
			t.Errorf("folder %q: got %q want %q", lang, got, want)
		}
	}
	for _, lang := range []string{"zh", "en", "cs", "ru"} {
		if journalTemplates[lang] == "" || journalFolderNames[lang] == "" {
			t.Errorf("missing %s defaults", lang)
		}
	}
	if localized(journalTemplates, "de") != journalTemplates["en"] {
		t.Error("unknown language should use the English template")
	}
}

// TestJournalConfigStoredValuesWin: an empty stored folder still means the default (as in older
// versions, which also sync this value), "/" is the notes root, and nothing is written on read.
func TestJournalConfigStoredValuesWin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{ctx: ctx, store: st}

	if cfg := a.GetJournalConfig("cs"); cfg.Folder != "Deník/YYYY/MM" || cfg.Name != defaultJournalName || cfg.Template != "" {
		t.Fatalf("unset cs: %+v", cfg)
	}
	if cfg := a.GetJournalConfig("zh-CN"); cfg.Folder != legacyJournalFolder {
		t.Fatalf("unset zh: %+v", cfg)
	}
	if _, err := st.StringSetting(ctx, settingJournalFolder); err == nil {
		t.Fatal("reading the journal config must not persist a default folder")
	}
	for stored, want := range map[string]string{legacyJournalFolder: legacyJournalFolder, "": "Deník/YYYY/MM", journalRootFolder: journalRootFolder} {
		if err := st.SetStringSetting(ctx, settingJournalFolder, stored); err != nil {
			t.Fatal(err)
		}
		if cfg := a.GetJournalConfig("cs"); cfg.Folder != want {
			t.Fatalf("stored %q: got %+v want %q", stored, cfg, want)
		}
	}
	if err := st.SetStringSetting(ctx, settingJournalName, ""); err != nil {
		t.Fatal(err)
	}
	if cfg := a.GetJournalConfig("cs"); cfg.Name != defaultJournalName {
		t.Fatalf("empty name should fall back: %+v", cfg)
	}
}

// TestSetJournalConfigFolder: "/" saves the notes root, an empty folder saves "" (default) and
// is returned expanded, other folders lose their surrounding slashes.
func TestSetJournalConfigFolder(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{ctx: ctx, store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, c := range []struct{ in, stored, shown string }{
		{"/", journalRootFolder, journalRootFolder},
		{" // ", journalRootFolder, journalRootFolder},
		{"", "", "Journal/YYYY/MM"},
		{"  ", "", "Journal/YYYY/MM"},
		{"/dailynote/YYYY/", "dailynote/YYYY", "dailynote/YYYY"},
	} {
		cfg, err := a.SetJournalConfig(JournalConfig{Folder: c.in}, "en")
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		got, _ := st.StringSetting(ctx, settingJournalFolder)
		if got != c.stored || cfg.Folder != c.shown {
			t.Errorf("%q: stored %q shown %q, want %q %q", c.in, got, cfg.Folder, c.stored, c.shown)
		}
	}
	folders, name, err := journalPath(JournalConfig{Folder: journalRootFolder, Name: defaultJournalName}, time.Date(2025, 3, 21, 0, 0, 0, 0, time.UTC), "en")
	if err != nil || len(folders) != 0 || name != "2025-03-21-Friday" {
		t.Fatalf("root journal path: %v %q %v", folders, name, err)
	}
}

// TestPickJournalFolder: an existing default folder in any language is kept, the legacy 日记 first.
func TestPickJournalFolder(t *testing.T) {
	has := func(names ...string) func(string) bool {
		return func(name string) bool { return slices.Contains(names, name) }
	}
	for _, c := range []struct {
		lang   string
		has    func(string) bool
		folder string
	}{
		{"cs", nil, "Deník/YYYY/MM"},
		{"cs", has(), "Deník/YYYY/MM"},
		{"en", has("日记", "Journal"), legacyJournalFolder},
		{"cs", has("Journal", "Deník"), "Deník/YYYY/MM"},
		{"zh", has("Journal"), "Journal/YYYY/MM"},
		{"fr", has("Дневник"), "Дневник/YYYY/MM"},
	} {
		if got := pickJournalFolder(c.lang, c.has); got != c.folder {
			t.Errorf("%s: got %q want %q", c.lang, got, c.folder)
		}
	}
}
