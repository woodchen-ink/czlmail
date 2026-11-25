package main

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// OpenLink 用系统默认程序打开邮件正文里的链接 (http / https 交给浏览器, mailto 交给邮件程序)。
//
// 界面不直接调 Wails 的 BrowserOpenURL: 它先过 ValidateAndSanitizeURL, 凡是含
// ~ ! ( ) * ; $ [ ] { } | 的地址一律丢弃, 不报错也没有提示。这些字符在 URL 里完全合法
// (~ 还是非保留字符), 不少通知与 newsletter 的跟踪链接都带, 结果就是点了没反应。
// 这里只放行 http / https / mailto, 其余协议拒绝并返回错误, 由界面提示。
func (a *App) OpenLink(raw string) error {
	u, ok := externalURL(raw)
	if !ok {
		a.log.Warn("open link: rejected")
		return fmt.Errorf("2227 link not allowed")
	}
	a.openURL(u)
	return nil
}

// OpenMeetingLink 打开日程里的会议链接。在 OpenLink 放行范围之外, 再放行常见会议程序的协议
// (msteams / zoommtg 等), 让「加入」能直接拉起客户端; file / javascript / ms-settings 等照旧拒绝。
func (a *App) OpenMeetingLink(raw string) error {
	u, ok := meetingURL(raw)
	if !ok {
		a.log.Warn("open meeting link: rejected")
		return fmt.Errorf("2227 link not allowed")
	}
	a.openURL(u)
	return nil
}

// meetingSchemes 是会议链接额外放行的程序协议, 全部小写, 写死不开放配置。
var meetingSchemes = map[string]bool{
	"msteams": true,
	"zoommtg": true,
	"zoomus":  true,
	"webex":   true,
	"wemeet":  true,
	"skype":   true,
}

func meetingURL(raw string) (string, bool) {
	if u, ok := externalURL(raw); ok {
		return u, true
	}
	if !cleanURLText(raw) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !meetingSchemes[strings.ToLower(u.Scheme)] || (u.Host == "" && u.Opaque == "" && u.Path == "") {
		return "", false
	}
	return u.String(), true
}

// mailtoParams 是 mailto 链接允许保留的参数。链接由发件人控制, 其余参数
// (如 attach= 会让部分邮件程序附上本机文件) 一律丢弃。
var mailtoParams = map[string]bool{"to": true, "cc": true, "bcc": true, "subject": true, "body": true}

func cleanURLText(raw string) bool {
	return raw != "" && !strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	})
}

// externalURL 校验一个要交给系统打开的地址, 返回规范化后的形式。
//
// 链接来自邮件正文或邮件头, 完全由发件人控制, 所以只认 http / https (必须有主机) 与 mailto。
// 控制字符与空白一律拒绝: 正常的地址里它们都已被百分号编码。
func externalURL(raw string) (string, bool) {
	if !cleanURLText(raw) {
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
		var kept []string
		for _, pair := range strings.Split(u.RawQuery, "&") {
			key, _, _ := strings.Cut(pair, "=")
			if mailtoParams[strings.ToLower(key)] {
				kept = append(kept, pair)
			}
		}
		u.RawQuery = strings.Join(kept, "&")
	default:
		return "", false
	}
	return u.String(), true
}
