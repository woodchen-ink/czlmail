package main

import "testing"

// TestDefaultTranslateLang: without a saved choice, AI translates into the UI language.
func TestDefaultTranslateLang(t *testing.T) {
	for locale, want := range map[string]string{"zh-CN": "简体中文", "en": "English", "cs": "Čeština", "ru": "Русский", "": "简体中文"} {
		if got := defaultTranslateLang(locale); got != want {
			t.Errorf("%q: got %q want %q", locale, got, want)
		}
	}
	a := &App{}
	a.SetUILocale("cs")
	if got := a.GetAIConfig().TranslateLang; got != "Čeština" {
		t.Errorf("GetAIConfig without a store: %q", got)
	}
}
