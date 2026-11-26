package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 同邮局用户目录: JMAP Principals(RFC 9670)列出服务器上的个人与群组, 写信时可以直接搜到同事。

// directoryCache 按 会话地址 + 服务器账号 id 记各登录的目录: 每个登录是一个邮局 (#15), 换了登录也不能沿用上一个邮局的目录。
// 锁只保护这张表, 查询在锁外做: 一个登录的服务器连不上时, 别的登录的目录与正在输入的收件人框都不跟着等。
var directoryCache struct {
	sync.Mutex
	entries map[string]*directoryEntry
}

type directoryEntry struct {
	at   time.Time
	list []store.Recipient
	// failed 表示上一次查询失败 (list 是更早一次成功的结果, 可能为空); 失败后 directoryRetry 内不再查。
	failed bool
	// fetching 表示正有一次查询在路上, 别的调用方直接用 list, 不另起一次。
	fetching bool
}

const (
	// directoryTTL 内复用上次取回的目录; 目录变化很少, 每次按键都请求服务器没有必要。
	directoryTTL = 10 * time.Minute
	// directoryRetry 是查询失败后的负缓存: 连不上的服务器不该每敲一个字重试一次。
	directoryRetry = time.Minute
	// directoryTimeout 是一次目录查询的上限。
	directoryTimeout = 8 * time.Second
)

// fetchDirectory 向服务器查一个账号所在邮局的目录, 测试里换成假的。
var fetchDirectory = func(a *App, ctx context.Context, rt accountRoute) ([]store.Recipient, error) {
	return a.loadDirectory(ctx, rt)
}

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

// directory 合并各登录的同邮局目录 (每个登录取一个有目录能力的个人账号, 并行), 主登录在前, 同一地址只列一次。
// 目录是整台服务器的, 同一登录下有多个个人账号 (czlmail 网关) 时取一个就够。
func (a *App) directory() []store.Recipient {
	accounts, err := a.ListAccounts()
	if err != nil {
		return nil
	}
	var targets []string
	signIns := map[string]bool{}
	for _, acc := range accounts {
		if acc.IsPersonal && acc.Features.Directory && !signIns[acc.SignIn] {
			signIns[acc.SignIn] = true
			targets = append(targets, acc.ID)
		}
	}
	lists := make([][]store.Recipient, len(targets))
	var wg sync.WaitGroup
	for i, acc := range targets {
		wg.Go(func() { lists[i] = a.directoryOf(acc) })
	}
	wg.Wait()
	var out []store.Recipient
	seen := map[string]bool{}
	for _, list := range lists {
		for _, r := range list {
			if key := strings.ToLower(r.Email); !seen[key] {
				seen[key] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// directoryOf 取一个账号所在邮局的目录: 缓存新鲜 (或刚失败过、正有查询在路上) 时直接用缓存, 否则在锁外查一次。
func (a *App) directoryOf(accountID string) []store.Recipient {
	// 不等启动时的会话恢复: 填收件人时不该卡住。
	rt, err := a.routeWithin(accountID, 0)
	if err != nil || rt.client == nil || rt.sync.Session() == nil {
		return nil
	}
	// 服务器没有成员目录 (Principals) 时不查: 否则每敲一个字都发一次注定失败的请求。
	if rt.sync.Lacks(accountID, jmapx.PrincipalsURI) {
		return nil
	}
	key := rt.client.SessionEndpoint + " " + rt.serverID

	directoryCache.Lock()
	if directoryCache.entries == nil {
		directoryCache.entries = map[string]*directoryEntry{}
	}
	e := directoryCache.entries[key]
	if e == nil {
		e = &directoryEntry{}
		directoryCache.entries[key] = e
	}
	fresh := !e.at.IsZero() && ((!e.failed && time.Since(e.at) < directoryTTL) || (e.failed && time.Since(e.at) < directoryRetry))
	if fresh || e.fetching {
		list := e.list
		directoryCache.Unlock()
		return list
	}
	e.fetching = true
	directoryCache.Unlock()

	ctx, cancel := context.WithTimeout(a.ctx, directoryTimeout)
	list, err := fetchDirectory(a, ctx, rt)
	cancel()

	directoryCache.Lock()
	defer directoryCache.Unlock()
	e.fetching, e.at = false, time.Now()
	if err != nil {
		a.log.Warn("load principal directory", "account", accountID, "err", err)
		e.failed = true // 沿用更早一次成功的结果
		return e.list
	}
	e.list, e.failed = list, false
	return list
}

// loadDirectory 用 Principal/get 取邮局里能收信的个人与群组。
func (a *App) loadDirectory(ctx context.Context, rt accountRoute) ([]store.Recipient, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&jmapx.Call{Method: "Principal/get", Args: map[string]any{
		"accountId":  rt.serverID,
		"ids":        nil,
		"properties": []string{"id", "type", "name", "email", "description"},
	}})
	resp, err := rt.do(req)
	if err != nil {
		return nil, err
	}
	got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok {
		return nil, errors.New("unexpected response to Principal/get")
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
	return out, nil
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
