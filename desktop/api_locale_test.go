package main

import "testing"

func TestSetUILocaleRejectsUnknown(t *testing.T) {
	a := &App{}
	if got := a.currentUILocale(); got != defaultUILocale {
		t.Fatalf("default locale = %q", got)
	}
	a.SetUILocale("cs")
	if got := a.currentUILocale(); got != "cs" {
		t.Fatalf("locale = %q, want cs", got)
	}
	a.SetUILocale("fr")
	if got := a.currentUILocale(); got != defaultUILocale {
		t.Fatalf("unknown locale kept as %q", got)
	}
}

func TestUpdateNoticeCoversEveryLocale(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en", "cs"} {
		if _, ok := updateNotices[locale]; !ok {
			t.Errorf("no update notice for %s", locale)
		}
	}
	if title, _ := updateNotice("en", "v1.2.3"); title != "CZL Mail v1.2.3 is available" {
		t.Errorf("en title = %q", title)
	}
	if title, _ := updateNotice("xx", "v1.2.3"); title != "CZL Mail v1.2.3 已发布" {
		t.Errorf("fallback title = %q", title)
	}
}
