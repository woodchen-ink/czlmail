// Package sieve 生成与解析邮件过滤规则脚本。
//
// 脚本开头用注释块保存规则的 JSON 元数据, 格式与 Bulwark 相同:
//
//	/* @metadata:begin
//	{"version":1,"rules":[...]}
//	@metadata:end */
//
// 这样两个客户端编辑的是同一套规则, 互相都能读回来。
// 没有元数据块的脚本(手写或其它客户端生成)只能用原始编辑器修改。
package sieve

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Rule 是一条过滤规则。
type Rule struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Enabled        bool        `json:"enabled"`
	MatchType      string      `json:"matchType"` // all / any
	Conditions     []Condition `json:"conditions"`
	Actions        []Action    `json:"actions"`
	StopProcessing bool        `json:"stopProcessing"`
}

// Condition 的 Value 在 JSON 里可以是字符串或字符串数组(数组表示任一匹配)。
type Condition struct {
	Field      string      `json:"field"`      // from to cc subject header size body attachment
	Comparator string      `json:"comparator"` // contains not_contains is not_is starts_with ends_with matches greater_than less_than has_any has_type
	Value      StringOrArr `json:"value"`
	HeaderName string      `json:"headerName,omitempty"`
}

// Action 是规则动作。
type Action struct {
	Type  string `json:"type"` // move copy forward mark_read star add_label discard reject keep stop
	Value string `json:"value,omitempty"`
}

// StringOrArr 兼容 "a" 与 ["a","b"] 两种写法, 输出时单值保持字符串以免改动 Bulwark 写入的内容。
type StringOrArr []string

func (s *StringOrArr) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

func (s StringOrArr) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

type metadata struct {
	Version  int             `json:"version"`
	Rules    []Rule          `json:"rules"`
	Vacation json.RawMessage `json:"vacation,omitempty"`
}

const (
	metaBegin = "/* @metadata:begin"
	metaEnd   = "@metadata:end */"
)

// ErrNoMetadata 表示脚本不是规则编辑器生成的。
var ErrNoMetadata = errors.New("script has no rule metadata")

// Parse 从脚本中读出规则。
func Parse(script string) ([]Rule, error) {
	start := strings.Index(script, metaBegin)
	end := strings.Index(script, metaEnd)
	if start < 0 || end < start {
		if strings.TrimSpace(script) == "" {
			return nil, nil
		}
		return nil, ErrNoMetadata
	}
	var m metadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(script[start+len(metaBegin):end])), &m); err != nil {
		return nil, fmt.Errorf("parse rule metadata: %w", err)
	}
	return m.Rules, nil
}

var headerMap = map[string]string{"from": "From", "to": "To", "cc": "Cc", "subject": "Subject"}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func stringArg(values []string, transform func(string) string) string {
	var out []string
	for _, v := range values {
		if v == "" {
			continue
		}
		if transform != nil {
			v = transform(v)
		}
		out = append(out, quote(v))
	}
	if len(out) == 1 {
		return out[0]
	}
	return "[" + strings.Join(out, ", ") + "]"
}

func condition(c Condition) (string, error) {
	values := []string(c.Value)
	switch c.Field {
	case "size":
		if len(values) == 0 {
			return "", fmt.Errorf("size condition needs a value")
		}
		op := ":under"
		if c.Comparator == "greater_than" {
			op = ":over"
		}
		return fmt.Sprintf("size %s %s", op, strings.TrimSpace(values[0])), nil
	case "body":
		match := ":contains"
		if c.Comparator == "is" {
			match = ":is"
		}
		return "body " + match + " " + stringArg(values, nil), nil
	case "attachment":
		if c.Comparator == "has_any" {
			return `header :mime :anychild :contains "Content-Disposition" "attachment"`, nil
		}
		return `header :mime :anychild :matches ["Content-Disposition", "Content-Type"] ` +
			stringArg(values, func(ext string) string { return "*." + strings.TrimLeft(ext, ".*") + "*" }), nil
	}
	if len(values) == 0 {
		return "", fmt.Errorf("condition on %s needs a value", c.Field)
	}
	header := headerMap[c.Field]
	if c.Field == "header" {
		header = c.HeaderName
		if header == "" {
			header = "X-Unknown"
		}
	}
	if header == "" {
		return "", fmt.Errorf("unknown field %q", c.Field)
	}
	h := quote(header)
	switch c.Comparator {
	case "not_contains":
		return "not header :contains " + h + " " + stringArg(values, nil), nil
	case "is":
		return "header :is " + h + " " + stringArg(values, nil), nil
	case "not_is":
		return "not header :is " + h + " " + stringArg(values, nil), nil
	case "starts_with":
		return "header :matches " + h + " " + stringArg(values, func(v string) string { return v + "*" }), nil
	case "ends_with":
		return "header :matches " + h + " " + stringArg(values, func(v string) string { return "*" + v }), nil
	case "matches":
		return "header :matches " + h + " " + stringArg(values, nil), nil
	default:
		return "header :contains " + h + " " + stringArg(values, nil), nil
	}
}

func action(a Action) (string, []string) {
	switch a.Type {
	case "move":
		return "fileinto " + quote(a.Value) + ";", []string{"fileinto"}
	case "copy":
		return "fileinto :copy " + quote(a.Value) + ";", []string{"fileinto", "copy"}
	case "forward":
		return "redirect " + quote(a.Value) + ";", nil
	case "mark_read":
		return `addflag "\\Seen";`, []string{"imap4flags"}
	case "star":
		return `addflag "\\Flagged";`, []string{"imap4flags"}
	case "add_label":
		return "addflag " + quote("$label:"+a.Value) + ";", []string{"imap4flags"}
	case "discard":
		return "discard;", nil
	case "reject":
		return "reject " + quote(a.Value) + ";", []string{"reject"}
	case "keep":
		return "keep;", nil
	case "stop":
		return "stop;", nil
	}
	return "", nil
}

// Generate 生成完整脚本。停用的规则只保存在元数据里。
func Generate(rules []Rule) (string, error) {
	if rules == nil {
		rules = []Rule{}
	}
	meta, err := json.Marshal(metadata{Version: 1, Rules: rules})
	if err != nil {
		return "", err
	}

	requires := map[string]bool{}
	var body []string
	for _, r := range rules {
		if !r.Enabled || len(r.Conditions) == 0 || len(r.Actions) == 0 {
			continue
		}
		var conds []string
		for _, c := range r.Conditions {
			s, err := condition(c)
			if err != nil {
				return "", fmt.Errorf("rule %q: %w", r.Name, err)
			}
			if c.Field == "body" {
				requires["body"] = true
			}
			if c.Field == "attachment" {
				requires["mime"] = true
			}
			conds = append(conds, s)
		}
		test := conds[0]
		if len(conds) > 1 {
			op := "allof"
			if r.MatchType == "any" {
				op = "anyof"
			}
			test = op + " (" + strings.Join(conds, ", ") + ")"
		}
		var acts []string
		for _, a := range r.Actions {
			s, req := action(a)
			if s == "" {
				continue
			}
			for _, x := range req {
				requires[x] = true
			}
			acts = append(acts, "    "+s)
		}
		if r.StopProcessing {
			acts = append(acts, "    stop;")
		}
		body = append(body, "", "# Rule: "+strings.ReplaceAll(r.Name, "\n", " "), "if "+test+" {", strings.Join(acts, "\n"), "}")
	}

	var lines []string
	lines = append(lines, metaBegin, string(meta), metaEnd, "")
	if len(requires) > 0 {
		var list []string
		for k := range requires {
			list = append(list, quote(k))
		}
		sort.Strings(list)
		lines = append(lines, "require ["+strings.Join(list, ", ")+"];")
	}
	lines = append(lines, body...)
	return strings.Join(lines, "\n") + "\n", nil
}
