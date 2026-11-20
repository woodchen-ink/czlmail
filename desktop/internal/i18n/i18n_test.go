package i18n

import (
	"testing"
	"testing/fstest"
)

func TestT(t *testing.T) {
	c, err := Load(fstest.MapFS{
		"en.json": {Data: []byte(`{"收到 {0} 封新邮件":"{0} new emails","由{0}检查{1}":"{1} checked by {0}"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ locale, source, want string }{
		{"en", "收到 {0} 封新邮件", "3 new emails"},
		{"en", "由{0}检查{1}", "b checked by {0}"},
		{"zh-CN", "收到 {0} 封新邮件", "收到 3 封新邮件"},
		{"en", "没有译文", "没有译文"},
		{"fr", "收到 {0} 封新邮件", "收到 3 封新邮件"},
	}
	for _, tc := range cases {
		args := []any{3}
		if tc.source == "由{0}检查{1}" {
			args = []any{"{0}", "b"}
		}
		if got := c.T(tc.locale, tc.source, args...); got != tc.want {
			t.Errorf("T(%s, %q) = %q, want %q", tc.locale, tc.source, got, tc.want)
		}
	}
	if !c.Has("en") || !c.Has(Source) || c.Has("fr") {
		t.Error("Has")
	}
}
