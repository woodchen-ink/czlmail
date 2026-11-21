//go:build windows

package main

import (
	_ "embed"
	"runtime"
	"sync"

	"github.com/energye/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
)

// 系统托盘。关闭窗口只是隐藏(见 main.go 的 HideWindowOnClose), 邮件客户端要常驻
// 才能实时收信与弹通知; 托盘是用户找回窗口与真正退出的入口。

// 托盘图标: 小尺寸符号的多帧 ico (16 / 20 / 24 / 32, 覆盖 100%-200% 缩放); 有未读时换成带角标的 32px 单帧。
//
//go:embed build/tray/tray.ico
var trayIcon []byte

// trayMenu 留着菜单项引用, 界面换语言时改标题 (setTrayMenuText)。托盘就绪前为 nil。
// 锁同时保护建菜单与改标题: 托盘线程建菜单时界面恰好上报语言, 改标题会等菜单建好再按最新语言写。
var trayMenu struct {
	sync.Mutex
	open, quit *systray.MenuItem
}

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

	trayMenu.Lock()
	defer trayMenu.Unlock()
	trayMenu.open = systray.AddMenuItem(a.t("打开 CZL Mail"), "")
	trayMenu.open.Click(a.showWindow)
	systray.AddSeparator()
	trayMenu.quit = systray.AddMenuItem(a.t("退出 CZL Mail"), "")
	trayMenu.quit.Click(func() {
		if a.ctx != nil {
			wruntime.Quit(a.ctx)
		}
	})
}

// setTrayMenuText 按界面语言改托盘菜单的文字。在锁内重新取文案: 连续切换语言时以最后一次为准。
func (a *App) setTrayMenuText() {
	trayMenu.Lock()
	defer trayMenu.Unlock()
	if trayMenu.open == nil || trayMenu.quit == nil {
		return
	}
	trayMenu.open.SetTitle(a.t("打开 CZL Mail"))
	trayMenu.quit.SetTitle(a.t("退出 CZL Mail"))
}

// setTrayUnread 更新托盘图标角标与提示文字。
func (a *App) setTrayUnread(n int) {
	if n > 0 {
		systray.SetIcon(badgeIcon(n))
		systray.SetTooltip(a.t("CZL Mail — {0} 封未读", n))
	} else {
		systray.SetIcon(trayIcon)
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
	platform.RaiseToForeground()
}
