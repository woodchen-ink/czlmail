package store

import (
	"regexp"
	"strings"
	"time"
)

// placeholderPattern 只匹配 {{标识符}}, 中间不允许空格。
//
// 严格匹配是有意的: 邮件正文里出现 {{ 的情况远不止模板变量(代码片段、
// 模板引擎的原样示例都会), 放宽规则会让这些内容被意外替换掉。
var placeholderPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// 内置占位符, 套用模板时自动填充, 无需用户输入。
const (
	PlaceholderDate       = "date"
	PlaceholderDayOfWeek  = "day_of_week"
	PlaceholderSenderName = "sender_name"
	PlaceholderRecipient  = "recipient_name"
)

// BuiltinPlaceholders 供界面区分"自动填充"与"需要你填"。
var BuiltinPlaceholders = []string{
	PlaceholderDate,
	PlaceholderDayOfWeek,
	PlaceholderSenderName,
	PlaceholderRecipient,
}

// ExtractPlaceholders 取出模板里出现的全部占位符, 去重并保持首次出现的顺序。
func ExtractPlaceholders(text string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(text, -1)

	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// PlaceholderContext 是自动填充内置占位符所需的上下文。
type PlaceholderContext struct {
	SenderName    string
	RecipientName string
	Now           time.Time
}

// ApplyPlaceholders 替换文本中的占位符。
//
// 未提供值的占位符**原样保留**而不是替换成空串: 一封发出去的信里留着
// {{company}} 固然难看, 但比悄无声息地少了一段话要好 —— 前者用户能看见并修正。
func ApplyPlaceholders(text string, values map[string]string, ctx PlaceholderContext) string {
	if ctx.Now.IsZero() {
		ctx.Now = time.Now()
	}

	builtin := map[string]string{
		PlaceholderDate:       ctx.Now.Format("2006-01-02"),
		PlaceholderDayOfWeek:  weekdayZh(ctx.Now.Weekday()),
		PlaceholderSenderName: ctx.SenderName,
		PlaceholderRecipient:  ctx.RecipientName,
	}

	return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")

		// 用户提供的值优先于内置值, 使得内置占位符也能被显式覆盖。
		if v, ok := values[name]; ok && v != "" {
			return v
		}
		if v, ok := builtin[name]; ok && v != "" {
			return v
		}
		return match
	})
}

func weekdayZh(d time.Weekday) string {
	names := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return names[int(d)%7]
}
