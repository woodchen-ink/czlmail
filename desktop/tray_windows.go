//go:build windows

package main

import (
	_ "embed"
	"runtime"
	"strconv"

	"github.com/energye/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 系统托盘。关闭窗口只是隐藏(见 main.go 的 HideWindowOnClose), 邮件客户端要常驻
// 才能实时收信与弹通知; 托盘是用户找回窗口与真正退出的入口。

//go:embed build/windows/icon.ico
var trayIcon []byte

// hideOnClose 报告关闭窗口时是否只隐藏。只有托盘可用的平台才能这样做,
// 否则窗口隐藏后用户再也找不回来。
const hideOnClose = true

func (a *App) startTray() {
	go func() {
		// 托盘窗口的创建与消息循环必须在同一个系统线程上。
		runtime.LockOSThread()
		systray.Run(a.onTrayReady, nil)
	}()
}

func (a *App) onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTooltip("CZL Mail")
	// 托盘就绪后按当前缓存显示一次未读数。
	a.refreshTrayUnreadSoon()
	systray.SetOnClick(func(systray.IMenu) { a.showWindow() })
	systray.SetOnRClick(func(menu systray.IMenu) { _ = menu.ShowMenu() })

	systray.AddMenuItem("打开 CZL Mail", "").Click(a.showWindow)
	systray.AddSeparator()
	systray.AddMenuItem("退出", "").Click(func() {
		if a.ctx != nil {
			wruntime.Quit(a.ctx)
		}
	})
}

// setTrayUnread 更新托盘图标角标与提示文字。
func (a *App) setTrayUnread(n int) {
	systray.SetIcon(badgeIcon(n))
	if n > 0 {
		systray.SetTooltip("CZL Mail — " + strconv.Itoa(n) + " 封未读")
	} else {
		systray.SetTooltip("CZL Mail")
	}
}

func (a *App) stopTray() {
	systray.Quit()
}

// showWindow 恢复被隐藏或最小化的窗口并置前。
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
	// 抢前台交给 raiseToForeground(见 window_windows.go)。
	// 不能用"置顶再取消"那一套: 取消没落地时窗口会永远压在别的程序上面。
	raiseToForeground()
}
