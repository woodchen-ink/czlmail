//go:build !windows

package platform

// MainWindowPlacement 只在 Windows 上实现; 其他平台由调用方退回 Wails 运行时的窗口尺寸。
func MainWindowPlacement() (WindowRect, bool, bool) { return WindowRect{}, false, false }

// PlaceMainWindow 只在 Windows 上实现。
func PlaceMainWindow(WindowRect) bool { return false }
