//go:build darwin

package main

import (
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// appMenu 是 macOS 顶部菜单栏。Wails 不给菜单时主菜单是空的, 而 AppKit 的 ⌘Q、⌘W、
// ⌘C/⌘V 等快捷键全靠菜单项的 keyEquivalent 分发, 没有菜单按下去就毫无反应。
func appMenu(a *App) *menu.Menu {
	m := menu.NewMenu()
	// 应用菜单: 隐藏(⌘H)、退出(⌘Q)。退出走 Wails 的正常关闭流程, 会触发 OnShutdown。
	m.Append(menu.AppMenu())

	file := m.AddSubmenu("文件")
	// ⌘W 与点窗口红色关闭按钮一致: 只隐藏(见 main.go 的 HideWindowOnClose), 继续收信。
	file.AddText("关闭窗口", keys.CmdOrCtrl("w"), func(*menu.CallbackData) {
		if a.ctx != nil {
			wruntime.Hide(a.ctx)
		}
	})

	// 编辑菜单: 让输入框与邮件正文里的 ⌘C/⌘V/⌘X/⌘A/⌘Z 生效。
	m.Append(menu.EditMenu())
	// 窗口菜单: 最小化(⌘M)、缩放、全屏(⌃⌘F)。
	m.Append(menu.WindowMenu())
	return m
}
