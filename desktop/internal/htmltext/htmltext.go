// Package htmltext 把邮件的 HTML 正文转成可读文本。
//
// 用途有三处, 要求都是"能读、能搜", 不是还原排版:
//   - 邮件只有 text/html 部件时, 落库的 body_text 由它生成(FTS 索引这一列,
//     索引 HTML 源码等于把标签和内联样式一起搜进去)
//   - 喂给模型的正文
//   - MCP 返回的正文
//
// 刻意不引第三方 HTML 解析器: 这里不需要构树, 逐段正则替换足够, 也避免为了
// 一个纯文本转换把 golang.org/x/net 拖进依赖。
package htmltext

import (
	"html"
	"regexp"
	"strings"
)

var (
	// 不可见部件整块丢掉, 否则 CSS 与脚本会被当成正文。
	// RE2 没有反向引用, 开闭标签只能各写一遍; 交叉配对在这里无害。
	reHidden = regexp.MustCompile(`(?is)<(script|style|head|title)\b[^>]*>.*?</(script|style|head|title)\s*>`)
	// 注释里常藏着营销邮件的条件注释。
	reComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// 块级元素的收尾换行, 否则整封信会连成一行。
	reBreak = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6]|/li|/blockquote|/table|/ul|/ol)\b[^>]*>`)
	reTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlank = regexp.MustCompile(`\n{3,}`)
	// 行尾空白与行内的连续空白: HTML 里的缩进换行不是内容。
	reSpaces  = regexp.MustCompile(`[ \t\x{00a0}]{2,}`)
	reTrailWS = regexp.MustCompile(`[ \t]+\n`)
)

// Convert 把 HTML 正文转成纯文本。输入不是 HTML 时原样返回(去掉首尾空白)。
func Convert(s string) string {
	s = reHidden.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")

	// 实体在去标签之后再解, 免得 "&lt;script&gt;" 解出来又被当成标签。
	// 用标准库而不是手写替换表: 邮件里 &#8203;、&hellip; 这类实体很常见。
	s = html.UnescapeString(s)

	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = reSpaces.ReplaceAllString(s, " ")
	s = reTrailWS.ReplaceAllString(s, "\n")
	return strings.TrimSpace(reBlank.ReplaceAllString(s, "\n\n"))
}
