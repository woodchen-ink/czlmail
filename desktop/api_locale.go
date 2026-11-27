package main

import (
	"context"
	"embed"
	"io/fs"

	"github.com/woodchen-ink/czlmail/desktop/internal/i18n"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// settingUILocale 记住界面最后一次告知的语言。界面每次启动都会再告知, 但在那之前后端可能已经发出通知
// (登录失效、提醒) 或画好托盘菜单, 没有它这些文字就是源语言 (中文)。
const settingUILocale = "uiLocale"

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
		a.rememberUILocale(locale)
		a.setTrayMenuText()
		a.redrawTrayUnread()
		a.planRemindersSoon(false)
		go a.refreshHandlers()
	}
}

// rememberUILocale 记下界面告知的语言, 下次启动时由 restoreUILocale 读回。
func (a *App) rememberUILocale(locale string) {
	st, err := a.currentStore()
	if err != nil {
		return
	}
	if err := st.SetStringSetting(context.Background(), settingUILocale, locale); err != nil {
		a.log.Warn("remember ui locale", "err", err)
	}
}

// restoreUILocale 在启动时先用上次记住的界面语言, 界面告知以后照常以界面为准。
// 界面已经告知过、没记住过或记住的语言没有词典时什么都不做; 返回是否换了语言。
func (a *App) restoreUILocale(ctx context.Context, st *store.Store) bool {
	saved, err := st.StringSetting(ctx, settingUILocale)
	if err != nil || saved == i18n.Source || !catalog.Has(saved) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.uiLocale != "" {
		return false
	}
	a.uiLocale = saved
	return true
}

// locale 是界面当前语言, 界面还没告知时为源语言。调用方持有 a.mu。
func (a *App) locale() string {
	if a.uiLocale == "" {
		return i18n.Source
	}
	return a.uiLocale
}

// currentLocale 是界面当前语言, 自己加锁。不能在持有 a.mu 时调用。
func (a *App) currentLocale() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.locale()
}

// t 按界面当前语言翻译后端文案, 规则同界面的 t()。不能在持有 a.mu 时调用。
func (a *App) t(source string, values ...any) string {
	return catalog.T(a.currentLocale(), source, values...)
}
