package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 同邮局用户目录: JMAP Principals(RFC 9670)列出服务器上的个人与群组, 写信时可以直接搜到同事。

var directoryCache struct {
	sync.Mutex
	at   time.Time
	list []store.Recipient
}

// directoryTTL 内复用上次取回的目录; 目录变化很少, 每次按键都请求服务器没有必要。
const directoryTTL = 10 * time.Minute

// SearchDirectory 在服务器用户目录里按姓名或地址搜索。服务器不支持或请求失败时返回空列表。
func (a *App) SearchDirectory(query string) ([]store.Recipient, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	list := a.directory()
	var out []store.Recipient
	for _, r := range list {
		if strings.Contains(strings.ToLower(r.Email), q) || strings.Contains(strings.ToLower(r.Name), q) {
			out = append(out, r)
			if len(out) >= 8 {
				break
			}
		}
	}
	return out, nil
}

func (a *App) directory() []store.Recipient {
	directoryCache.Lock()
	defer directoryCache.Unlock()
	if directoryCache.list != nil && time.Since(directoryCache.at) < directoryTTL {
		return directoryCache.list
	}
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client == nil || client.Session == nil {
		return nil
	}
	accountID := ""
	if st, err := a.currentStore(); err == nil {
		accountID = a.personalAccountID(st)
	}
	if accountID == "" {
		return nil
	}

	req := &jmap.Request{Context: a.ctx}
	req.Invoke(&jmapx.Call{Method: "Principal/get", Args: map[string]any{
		"accountId":  accountID,
		"ids":        nil,
		"properties": []string{"id", "type", "name", "email", "description"},
	}})
	resp, err := a.doJMAP(req)
	if err != nil {
		a.log.Warn("load principal directory", "err", err)
		return nil
	}
	got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok {
		return nil
	}
	out := make([]store.Recipient, 0, len(got.List))
	for _, raw := range got.List {
		var p struct {
			Type        string `json:"type"`
			Name        string `json:"name"`
			Email       string `json:"email"`
			Description string `json:"description"`
		}
		if json.Unmarshal(raw, &p) != nil || !strings.Contains(p.Email, "@") {
			continue
		}
		// 个人与群组(共享邮箱、分发组)都能收信; 资源、地点这类不列。
		if p.Type != "" && p.Type != "individual" && p.Type != "group" {
			continue
		}
		out = append(out, store.Recipient{Name: principalName(p.Name, p.Description, p.Email), Email: p.Email})
	}
	directoryCache.list, directoryCache.at = out, time.Now()
	return out
}

// principalName 选显示名: Stalwart 的 name 是登录名(通常就是地址), 真实姓名多写在 description 里;
// 但 description 也常被当备注用("仅程序使用"之类), 太长的不当姓名。
func principalName(name, description, email string) string {
	d := strings.TrimSpace(description)
	if d != "" && len([]rune(d)) <= 20 && !strings.ContainsAny(d, "()（）,，") {
		return d
	}
	if name != "" && !strings.EqualFold(name, email) {
		return name
	}
	return ""
}
