package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
)

// 系统集成的界面接口, 以及从命令行打开的 mailto: 链接与 .ics 文件。

// EventOpenRequest 有新的外部打开请求(mailto 或 ics), 界面收到后调用 TakeOpenRequests。
const EventOpenRequest = "app:open-request"

// IntegrationStatus 是设置页与首次引导用的状态。
type IntegrationStatus struct {
	// Platform 是 runtime.GOOS, 界面按平台给出不同的说明(Windows 要去系统设置确认, macOS 直接设置)。
	Platform        string `json:"platform"`
	Supported       bool   `json:"supported"`
	Autostart       bool   `json:"autostart"`
	DefaultMail     bool   `json:"defaultMail"`
	DefaultCalendar bool   `json:"defaultCalendar"`
}

func (a *App) GetIntegrationStatus() IntegrationStatus {
	return IntegrationStatus{
		Platform: runtime.GOOS, Supported: platform.IntegrationSupported, Autostart: platform.AutostartEnabled(),
		DefaultMail: platform.IsDefaultMail(), DefaultCalendar: platform.IsDefaultCalendar(),
	}
}

func (a *App) SetAutostart(on bool) (IntegrationStatus, error) {
	err := platform.SetAutostart(on)
	return a.GetIntegrationStatus(), err
}

// OpenDefaultAppsSettings 打开系统的默认应用设置页; macOS 上直接设为默认。
func (a *App) OpenDefaultAppsSettings() error {
	if err := platform.RegisterHandlers(); err != nil {
		return err
	}
	return platform.OpenDefaultAppsSettings()
}

// OpenRequest 是一个外部打开请求。
type OpenRequest struct {
	// Kind: compose / ics / email / thread
	Kind    string `json:"kind"`
	To      string `json:"to"`
	CC      string `json:"cc"`
	BCC     string `json:"bcc"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// Path 是本地 .ics 文件, 或 webcal 订阅地址(已转换为 https)。
	Path string `json:"path"`
	// AccountID 与 ID 是 czlmail:// 链接指向的邮件或会话。
	AccountID string `json:"accountId"`
	ID        string `json:"id"`
}

type openQueue struct {
	mu      sync.Mutex
	pending []OpenRequest
}

// handleArgs 解析命令行参数中的 mailto: 与 .ics, 入队并通知界面。
// 首次启动时界面可能还没加载, 请求留在队列里, 由界面加载完成后主动来取。
func (a *App) handleArgs(args []string) bool {
	var reqs []OpenRequest
	for _, arg := range args {
		lower := strings.ToLower(arg)
		switch {
		case strings.HasPrefix(lower, "mailto:"):
			reqs = append(reqs, parseMailto(arg))
		case strings.HasPrefix(lower, platform.LinkScheme+"://"):
			if req, ok := parseMailLink(arg); ok {
				reqs = append(reqs, req)
			}
		case strings.HasPrefix(lower, "webcal://"), strings.HasPrefix(lower, "webcals://"):
			reqs = append(reqs, OpenRequest{Kind: "ics", Path: "https://" + arg[strings.Index(arg, "://")+3:]})
		case strings.HasSuffix(lower, ".ics"):
			if abs, err := filepath.Abs(arg); err == nil {
				if _, err := os.Stat(abs); err == nil {
					reqs = append(reqs, OpenRequest{Kind: "ics", Path: abs})
				}
			}
		}
	}
	if len(reqs) == 0 {
		return false
	}
	a.opens.mu.Lock()
	a.opens.pending = append(a.opens.pending, reqs...)
	a.opens.mu.Unlock()
	a.showWindow()
	a.emit(EventOpenRequest)
	return true
}

// TakeOpenRequests 取出并清空待处理的外部打开请求。
func (a *App) TakeOpenRequests() []OpenRequest {
	a.opens.mu.Lock()
	defer a.opens.mu.Unlock()
	out := a.opens.pending
	a.opens.pending = nil
	return out
}

// parseMailLink 解析 czlmail:// 链接。
func parseMailLink(raw string) (OpenRequest, bool) {
	rest := raw[len(platform.LinkScheme+"://"):]
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		return OpenRequest{}, false
	}
	kind := strings.ToLower(parts[0])
	if kind != "email" && kind != "thread" {
		return OpenRequest{}, false
	}
	acc, err1 := url.PathUnescape(parts[1])
	id, err2 := url.PathUnescape(parts[2])
	if err1 != nil || err2 != nil || acc == "" || id == "" {
		return OpenRequest{}, false
	}
	return OpenRequest{Kind: kind, AccountID: acc, ID: id}, true
}

// parseMailto 解析 RFC 6068 mailto 链接。
func parseMailto(raw string) OpenRequest {
	req := OpenRequest{Kind: "compose"}
	rest := raw[len("mailto:"):]
	addr, query, _ := strings.Cut(rest, "?")
	if to, err := url.PathUnescape(addr); err == nil {
		req.To = to
	}
	values, _ := url.ParseQuery(query)
	for k, v := range values {
		if len(v) == 0 {
			continue
		}
		joined := strings.Join(v, ", ")
		switch strings.ToLower(k) {
		case "to":
			req.To = strings.Trim(req.To+", "+joined, ", ")
		case "cc":
			req.CC = joined
		case "bcc":
			req.BCC = joined
		case "subject":
			req.Subject = v[0]
		case "body":
			req.Body = v[0]
		}
	}
	return req
}
