package main

import (
	"context"
	"path/filepath"
	"testing"

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

// TestJournalConfigStoredValuesWin: a saved folder, including an empty one for the notes root, is never replaced by a default.
func TestJournalConfigStoredValuesWin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{ctx: ctx, store: st}

	if cfg, set := a.journalConfig("cs"); set || cfg.Folder != "Deník/YYYY/MM" || cfg.Name != defaultJournalName || cfg.Template != "" {
		t.Fatalf("unset cs: %v %+v", set, cfg)
	}
	if cfg := a.GetJournalConfig("zh-CN"); cfg.Folder != legacyJournalFolder {
		t.Fatalf("unset zh: %+v", cfg)
	}
	for _, folder := range []string{legacyJournalFolder, ""} {
		if err := st.SetStringSetting(ctx, settingJournalFolder, folder); err != nil {
			t.Fatal(err)
		}
		if cfg, set := a.journalConfig("cs"); !set || cfg.Folder != folder {
			t.Fatalf("stored %q: %v %+v", folder, set, cfg)
		}
	}
	if err := st.SetStringSetting(ctx, settingJournalName, ""); err != nil {
		t.Fatal(err)
	}
	if cfg := a.GetJournalConfig("cs"); cfg.Name != defaultJournalName {
		t.Fatalf("empty name should fall back: %+v", cfg)
	}
}
