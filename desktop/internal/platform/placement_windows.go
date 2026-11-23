//go:build windows

package platform

import "unsafe"

// 主窗口位置的读写, 供下次启动时还原。
//
// 用 GetWindowPlacement / SetWindowPlacement 而不是 Wails 的 WindowGetPosition / WindowSetPosition:
// 后两者在 Windows 上坐标系不一致(读的是屏幕坐标, 写的是相对当前显示器工作区),
// 多显示器时还原会落到别的屏幕上; 而且窗口最大化时它们拿到的是最大化后的尺寸,
// 还原后再点「向下还原」窗口就没有原来的大小可回了。
// WINDOWPLACEMENT 里的 rcNormalPosition 正是「向下还原」时的位置与大小, 最大化、最小化时也保持不变。

var (
	procGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement = user32.NewProc("SetWindowPlacement")
	procIsZoomed           = user32.NewProc("IsZoomed")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procMonitorFromRect    = user32.NewProc("MonitorFromRect")
)

const (
	swHide                = 0
	swShowMinimized       = 2
	wpfRestoreToMaximized = 2
	monitorDefaultToNull  = 0
	// 标题栏至少这么高的一条要落在某个显示器上, 窗口才算还能被抓回来。
	titleStrip = 32
)

type point struct{ X, Y int32 }

type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    point
	PtMaxPosition    point
	RcNormalPosition WindowRect
}

func getPlacement(hwnd uintptr) (windowPlacement, bool) {
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	ok, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp, ok != 0
}

// MainWindowPlacement 返回主窗口「向下还原」时的位置与大小(工作区坐标, 物理像素),
// 以及窗口当前是否最大化。窗口藏在托盘或最小化时也照样有效。
func MainWindowPlacement() (WindowRect, bool, bool) {
	hwnd := findMainWindow()
	if hwnd == 0 {
		return WindowRect{}, false, false
	}
	wp, ok := getPlacement(hwnd)
	if !ok || wp.RcNormalPosition.empty() {
		return WindowRect{}, false, false
	}
	// IsZoomed 看的是 WS_MAXIMIZE 样式, 窗口隐藏(缩到托盘)后依旧准确;
	// 最大化后再最小化的窗口要看 WPF_RESTORETOMAXIMIZED。
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	maximized := zoomed != 0 || (wp.ShowCmd == swShowMinimized && wp.Flags&wpfRestoreToMaximized != 0)
	return wp.RcNormalPosition, maximized, true
}

// PlaceMainWindow 把主窗口「向下还原」时的位置与大小设为 r, 不改变窗口当前是否可见。
// r 已不在任何显示器上(拔了外接屏、改了分辨率)时什么也不做, 窗口留在系统默认位置, 返回 false。
func PlaceMainWindow(r WindowRect) bool {
	if r.empty() || !onSomeMonitor(r) {
		return false
	}
	hwnd := findMainWindow()
	if hwnd == 0 {
		return false
	}
	wp, ok := getPlacement(hwnd)
	if !ok {
		return false
	}
	// 隐藏窗口的 GetWindowPlacement 照样报 SW_SHOWNORMAL, 原样写回会把窗口显示出来;
	// 启动时窗口还没显示(Wails 等页面加载完才显示), 必须保持隐藏。
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		wp.ShowCmd = swHide
	}
	wp.Flags = 0
	wp.RcNormalPosition = r
	set, _, _ := procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return set != 0
}

func onSomeMonitor(r WindowRect) bool {
	strip := WindowRect{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: min(r.Bottom, r.Top+titleStrip)}
	m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&strip)), monitorDefaultToNull)
	return m != 0
}
