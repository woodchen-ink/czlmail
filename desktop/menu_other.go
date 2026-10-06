//go:build !darwin

package main

import "github.com/wailsapp/wails/v2/pkg/menu"

// Windows 与 Linux 不显示窗口菜单栏, 快捷键由 WebView 自己处理。
func appMenu(*App) *menu.Menu { return nil }
