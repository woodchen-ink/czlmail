package main

import (
	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// requireCapability 在账号所在的服务器明确没有公布某能力时返回 2280 (capability.UnsupportedError)。
// 以正在用的会话为准; 会话还没恢复或账号不属于任何登录时不拦, 交给后续调用按原有路径报错。
func (a *App) requireCapability(accountID string, uri jmap.URI) error {
	rt, err := a.routeWithin(accountID, 0)
	if err != nil {
		return nil
	}
	return rt.sync.Require(accountID, uri)
}

// requireCalendars / requireFiles 是 MCP 工具用的简写: 没有日历或网盘的账号返回带码的英文说明, 而不是空列表或含糊的错误。
func (a *App) requireCalendars(accountID string) error {
	return a.requireCapability(accountID, jmapx.CalendarsURI)
}

func (a *App) requireFiles(accountID string) error {
	return a.requireCapability(accountID, jmapx.FileNodeURI)
}
