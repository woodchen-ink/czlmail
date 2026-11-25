package main

import (
	"net/url"
	"strings"
	"unicode"
)

// OpenLink 用系统默认程序打开邮件正文里的链接 (http / https 交给浏览器, mailto 交给邮件程序)。
//
// 界面不直接调 Wails 的 BrowserOpenURL: 它先过 ValidateAndSanitizeURL, 凡是含
// ~ ! ( ) * ; $ [ ] { } | 的地址一律丢弃, 不报错也没有提示。这些字符在 URL 里完全合法
// (~ 还是非保留字符), 不少通知与 newsletter 的跟踪链接都带, 结果就是点了没反应。
// 这里只放行 http / https / mailto, 其余协议照旧拒绝。
func (a *App) OpenLink(raw string) {
	u, ok := externalURL(raw)
	if !ok {
		a.log.Warn("open link: rejected")
		return
	}
	a.openURL(u)
}

// externalURL 校验一个要交给系统打开的地址, 返回规范化后的形式。
//
// 链接来自邮件正文或邮件头, 完全由发件人控制, 所以只认 http / https (必须有主机) 与 mailto。
// 控制字符与空白一律拒绝: 正常的地址里它们都已被百分号编码。
func externalURL(raw string) (string, bool) {
	if raw == "" || strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return "", false
		}
	case "mailto":
		if u.Opaque == "" {
			return "", false
		}
	default:
		return "", false
	}
	return u.String(), true
}
