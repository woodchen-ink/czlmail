//go:build !windows

package main

import wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

// 非 Windows 平台暂无托盘: macOS 的托盘库与 Wails 争用 NSApplication 主循环。
// 没有托盘时关闭窗口必须真正关闭, 否则隐藏后无从找回。
const hideOnClose = false

func (a *App) startTray() {}

func (a *App) stopTray() {}

func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
}
