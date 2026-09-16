package sieve

import (
	"strings"
	"testing"
)

// Bulwark 写入的真实脚本应能读回规则。
const bulwarkScript = `/* @metadata:begin
{"version":1,"rules":[{"id":"1cbed157","name":"垃圾邮件","enabled":true,"matchType":"any","conditions":[{"field":"from","comparator":"contains","value":"17728013197"}],"actions":[{"type":"discard"}],"stopProcessing":false}]}
@metadata:end */

require ["imap4flags"];

# Rule: 垃圾邮件
if header :contains "From" "17728013197" {
    discard;
}`

func TestParseBulwark(t *testing.T) {
	rules, err := Parse(bulwarkScript)
	if err != nil || len(rules) != 1 || rules[0].Name != "垃圾邮件" || rules[0].Conditions[0].Value[0] != "17728013197" {
		t.Fatalf("%+v %v", rules, err)
	}
	if _, err := Parse(`if true { keep; }`); err != ErrNoMetadata {
		t.Errorf("hand-written script should report ErrNoMetadata, got %v", err)
	}
}

func TestGenerateRoundTrip(t *testing.T) {
	rules := []Rule{
		{ID: "a", Name: "发票", Enabled: true, MatchType: "all",
			Conditions: []Condition{
				{Field: "subject", Comparator: "contains", Value: StringOrArr{"发票"}},
				{Field: "from", Comparator: "ends_with", Value: StringOrArr{"@a.com", "@b.com"}},
			},
			Actions: []Action{{Type: "move", Value: "财务"}, {Type: "mark_read"}}, StopProcessing: true},
		{ID: "b", Name: "停用", Enabled: false, MatchType: "any",
			Conditions: []Condition{{Field: "body", Comparator: "contains", Value: StringOrArr{"x"}}},
			Actions:    []Action{{Type: "discard"}}},
	}
	script, err := Generate(rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`require ["fileinto", "imap4flags"];`,
		`if allof (header :contains "Subject" "发票", header :matches "From" ["*@a.com", "*@b.com"]) {`,
		`fileinto "财务";`, `addflag "\\Seen";`, "stop;",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q in:\n%s", want, script)
		}
	}
	if strings.Contains(script, "body :contains") {
		t.Error("disabled rule must not be emitted")
	}
	back, err := Parse(script)
	if err != nil || len(back) != 2 || back[0].Conditions[1].Value[1] != "@b.com" || back[1].Enabled {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	// 单值条件在元数据里仍是字符串, 与 Bulwark 写入的格式一致。
	if !strings.Contains(script, `"value":"发票"`) {
		t.Error("single value should stay a JSON string")
	}
}
