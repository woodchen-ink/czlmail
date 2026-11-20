package main

// defaultUILocale 是界面还没告知语言时的回落, 与前端 lib/i18n 的默认一致。
const defaultUILocale = "zh-CN"

// SetUILocale 由界面在启动与切换语言时告知当前语言, 后端据此挑选系统通知等少量自己发出的文案。
// 只认前端 lib/i18n 支持的语言, 其余按默认处理。
func (a *App) SetUILocale(locale string) {
	switch locale {
	case "zh-CN", "en", "cs":
	default:
		locale = defaultUILocale
	}
	a.mu.Lock()
	a.uiLocale = locale
	a.mu.Unlock()
}

// currentUILocale 返回界面当前语言; 界面还没告知时为默认语言。
func (a *App) currentUILocale() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.uiLocale == "" {
		return defaultUILocale
	}
	return a.uiLocale
}
