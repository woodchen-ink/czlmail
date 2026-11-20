package main

import (
	"embed"
	"io/fs"

	"github.com/woodchen-ink/czlmail/desktop/internal/i18n"
)

// 后端自己展示给用户的文案 (系统通知、托盘菜单、文件对话框) 与界面共用 frontend/lib/i18n 的语言文件:
// 写 a.t("中文原文", 参数...), 原文就是词典的 key, 新原文要同时加进每个 <id>.json (go test 会检查)。

//go:embed frontend/lib/i18n/*.json
var localeFiles embed.FS

var catalog = func() *i18n.Catalog {
	sub, err := fs.Sub(localeFiles, "frontend/lib/i18n")
	if err != nil {
		panic(err)
	}
	c, err := i18n.Load(sub)
	if err != nil {
		panic(err) // 词典随程序编进来, 坏了是构建问题, 不是运行时情况。
	}
	return c
}()

// SetUILocale 由界面在启动与切换语言时告知当前语言; 没有词典的语言按源语言 (中文) 处理。
// 语言变了时刷新已经显示出来的托盘文字, 并按新文案重新登记移动端的定时提醒。
func (a *App) SetUILocale(locale string) {
	if !catalog.Has(locale) {
		locale = i18n.Source
	}
	a.mu.Lock()
	changed := locale != a.locale()
	a.uiLocale = locale
	a.mu.Unlock()
	// 每次都交给宿主: 原生壳刚装好时还没存过, 只在变化时发会漏掉第一次。
	a.reportUILocale(locale)
	if changed {
		a.setTrayMenuText()
		a.redrawTrayUnread()
		a.planRemindersSoon(false)
	}
}

// locale 是界面当前语言, 界面还没告知时为源语言。调用方持有 a.mu。
func (a *App) locale() string {
	if a.uiLocale == "" {
		return i18n.Source
	}
	return a.uiLocale
}

// t 按界面当前语言翻译后端文案, 规则同界面的 t()。不能在持有 a.mu 时调用。
func (a *App) t(source string, values ...any) string {
	a.mu.RLock()
	locale := a.locale()
	a.mu.RUnlock()
	return catalog.T(locale, source, values...)
}
