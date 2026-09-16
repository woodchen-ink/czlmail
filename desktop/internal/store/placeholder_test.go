package store

import (
	"testing"
	"time"
)

func TestExtractPlaceholders(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"你好 {{name}}", []string{"name"}},
		{"{{greeting}} {{name}}, 欢迎来到 {{company}}", []string{"greeting", "name", "company"}},
		{"{{name}} 与 {{name}}", []string{"name"}},
		// 带空格、单括号、空占位符都不算变量 —— 正文里出现 {{ 的场景很多,
		// 放宽规则会误伤代码片段一类的内容。
		{"{{}} {name} {{ name }}", nil},
		{"没有占位符", nil},
	}

	for _, c := range cases {
		got := ExtractPlaceholders(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ExtractPlaceholders(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ExtractPlaceholders(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestApplyPlaceholdersBuiltins(t *testing.T) {
	ctx := PlaceholderContext{
		SenderName:    "wood",
		RecipientName: "客户",
		Now:           time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC), // 周三
	}

	got := ApplyPlaceholders("{{recipient_name}}你好，今天是 {{date}}（{{day_of_week}}），{{sender_name}}", nil, ctx)
	want := "客户你好，今天是 2026-09-16（周三），wood"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// 未提供值的占位符必须原样保留 —— 替换成空串会让发出去的信悄悄少一段话。
func TestApplyPlaceholdersKeepsUnknown(t *testing.T) {
	got := ApplyPlaceholders("合同编号 {{contract_no}}", nil, PlaceholderContext{})
	if got != "合同编号 {{contract_no}}" {
		t.Errorf("未知占位符应原样保留, 实际 %q", got)
	}
}

// 用户提供的值优先于内置值。
func TestApplyPlaceholdersUserOverridesBuiltin(t *testing.T) {
	ctx := PlaceholderContext{SenderName: "wood", Now: time.Now()}
	got := ApplyPlaceholders("{{sender_name}}", map[string]string{"sender_name": "CZL 客服"}, ctx)
	if got != "CZL 客服" {
		t.Errorf("用户值应优先, 实际 %q", got)
	}
}
