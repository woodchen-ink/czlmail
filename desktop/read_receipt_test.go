package main

import (
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/i18n"
)

// TestReadReceiptText: the receipt is written in the UI language, with an English copy unless that is English already.
func TestReadReceiptText(t *testing.T) {
	cases := map[string]struct{ subject, first string }{
		i18n.Source: {"已读：Nabídka", "您发送的邮件已被阅读。"},
		"cs":        {"Přečteno: Nabídka", "Vaše zpráva byla přečtena."},
		"en":        {"Read: Nabídka", "Your message has been read."},
		"ru":        {"Прочитано: Nabídka", "Ваше сообщение прочитано."},
	}
	for locale, want := range cases {
		tr := func(source string, values ...any) string { return catalog.T(locale, source, values...) }
		subject, body := readReceiptText(tr, locale, "Nabídka", "me@example.com", "2026-11-25 18:40")
		if subject != want.subject || !strings.HasPrefix(body, want.first+"\n\n") {
			t.Errorf("%s: %q / %q", locale, subject, body)
		}
		if !strings.Contains(body, "me@example.com") || !strings.Contains(body, "2026-11-25 18:40") {
			t.Errorf("%s: reader or time missing: %q", locale, body)
		}
		if english := strings.Contains(body, "This is a receipt for the mail you sent."); english == (locale == "en") {
			t.Errorf("%s: english copy %v: %q", locale, english, body)
		}
	}
}
