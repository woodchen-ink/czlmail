package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSetUILocaleRejectsUnknown(t *testing.T) {
	a := &App{}
	if got := a.text(); got != uiTexts[defaultUILocale] {
		t.Fatal("text before the UI reports a locale is not the default")
	}
	a.SetUILocale("cs")
	if got := a.text(); got != uiTexts["cs"] {
		t.Fatal("cs not applied")
	}
	a.SetUILocale("fr")
	if got := a.text(); got != uiTexts[defaultUILocale] {
		t.Fatal("unknown locale not reset to default")
	}
}

// 每种语言的每个字段都要填, 格式占位符与默认语言一致: 漏填会在通知里出现空标题, 占位符不符会打出 %!d。
func TestUITextsComplete(t *testing.T) {
	base := reflect.ValueOf(uiTexts[defaultUILocale])
	for locale, text := range uiTexts {
		v := reflect.ValueOf(text)
		for i := range v.NumField() {
			name := v.Type().Field(i).Name
			got, want := v.Field(i).String(), base.Field(i).String()
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s.%s is empty", locale, name)
			}
			for _, verb := range []string{"%s", "%d"} {
				if strings.Count(got, verb) != strings.Count(want, verb) {
					t.Errorf("%s.%s: %s count differs from %s", locale, name, verb, defaultUILocale)
				}
			}
		}
	}
}
