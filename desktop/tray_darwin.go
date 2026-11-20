//go:build darwin && !ios

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>

void czlTrayStart(const void *png, int length, const char *open, const char *quit);
void czlTrayStop(void);
void czlTraySetMenuText(const char *open, const char *quit);
void czlTraySetUnread(int n, const char *tooltip);
void czlActivateApp(void);
*/
import "C"

import (
	"sync/atomic"
	"unsafe"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// macOS 菜单栏图标(NSStatusItem), 实现在 tray_darwin.m。
//
// 不用 energye/systray: 它要自己跑 NSApplication 主循环, 与 Wails 冲突。
// 这里只往 Wails 已经在跑的主线程上挂一个状态栏项, 另外补上 Dock 图标点击时恢复窗口 ——
// Wails 的 AppDelegate 没有实现 applicationShouldHandleReopen, 窗口隐藏后点 Dock 不会出来。

// hideOnClose: 有菜单栏图标与 Dock 可以找回窗口, 关闭窗口只隐藏, 继续收信。
const hideOnClose = true

var trayApp atomic.Pointer[App]

func (a *App) startTray() {
	trayApp.Store(a)
	// 1024px 的应用图标, 由 AppKit 缩到菜单栏尺寸(Retina 下取 2 倍像素)。C 侧拷贝后释放。
	open, quit := C.CString(a.t("打开 CZL Mail")), C.CString(a.t("退出 CZL Mail"))
	defer C.free(unsafe.Pointer(open))
	defer C.free(unsafe.Pointer(quit))
	C.czlTrayStart(C.CBytes(appIconPNG), C.int(len(appIconPNG)), open, quit)
	// 菜单栏就绪后按当前缓存显示一次未读数。
	a.refreshTrayUnreadSoon()
}

func (a *App) stopTray() {
	C.czlTrayStop()
}

// setTrayMenuText 按界面语言改菜单栏图标菜单的文字。C 侧同步拷贝字符串, 调用返回后即可释放。
func (a *App) setTrayMenuText() {
	open, quit := C.CString(a.t("打开 CZL Mail")), C.CString(a.t("退出 CZL Mail"))
	defer C.free(unsafe.Pointer(open))
	defer C.free(unsafe.Pointer(quit))
	C.czlTraySetMenuText(open, quit)
}

// setTrayUnread 在菜单栏图标旁与 Dock 图标上显示未读数, 悬停提示按界面语言写。
func (a *App) setTrayUnread(n int) {
	tooltip := "CZL Mail"
	if n > 0 {
		tooltip = a.t("CZL Mail — {0} 封未读", n)
	}
	cs := C.CString(tooltip)
	defer C.free(unsafe.Pointer(cs))
	C.czlTraySetUnread(C.int(n), cs)
}

// showWindow 恢复被隐藏或最小化的窗口并把程序切到前台。
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
	C.czlActivateApp()
}

// 以下由 Objective-C 在主线程回调。Wails 的窗口操作本身会派发回主线程,
// 放到协程里执行, 不在主线程的回调栈里调用它们。

//export czlTrayOpen
func czlTrayOpen() {
	if a := trayApp.Load(); a != nil {
		go a.showWindow()
	}
}

//export czlTrayQuit
func czlTrayQuit() {
	if a := trayApp.Load(); a != nil && a.ctx != nil {
		go wruntime.Quit(a.ctx)
	}
}
