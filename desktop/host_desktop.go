//go:build !ios && !android

package main

import (
	"errors"

	"github.com/pkg/browser"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// isMobile 区分桌面端与移动端 (iOS / Android) 的少数行为差异: MCP、附件落地目录。
const isMobile = false

func wailsFilters(fs []fileFilter) []wruntime.FileFilter {
	out := make([]wruntime.FileFilter, 0, len(fs))
	for _, f := range fs {
		out = append(out, wruntime.FileFilter{DisplayName: f.Name, Pattern: f.Pattern})
	}
	return out
}

func (a *App) saveFileDialog(d fileDialog) (string, error) {
	return wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           d.Title,
		DefaultFilename: d.DefaultFilename,
		Filters:         wailsFilters(d.Filters),
	})
}

func (a *App) openFileDialog(d fileDialog) (string, error) {
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{Title: d.Title, Filters: wailsFilters(d.Filters)})
}

func (a *App) openFilesDialog(d fileDialog) ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: d.Title, Filters: wailsFilters(d.Filters)})
}

func (a *App) openDirectoryDialog(d fileDialog) (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: d.Title})
}

// openURL 不用 wruntime.BrowserOpenURL, 原因见 OpenLink。pkg/browser 在 Windows 上
// 直接 ShellExecute 这个地址, 不经过命令行解释器。调用方负责先过 externalURL / meetingURL。
func (a *App) openURL(u string) {
	go func() {
		if err := browser.OpenURL(u); err != nil {
			a.log.Warn("open url", "err", err)
		}
	}()
}

func (a *App) quit() { wruntime.Quit(a.ctx) }

// emit 向前端广播事件。启动早期 ctx 尚未就绪时静默丢弃 ——
// 此时窗口还没有监听者, 事件本身也没有意义。
func (a *App) emit(name string, data ...any) {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, name, data...)
}

// installPackage 只在 Android 上使用; 桌面端的安装逻辑在 InstallUpdate 里。
func (a *App) installPackage(string) error {
	return errors.New("2154 installPackage is only supported on Android")
}

// reportUILocale 只有 iOS 的通知扩展需要 (host_ios.go)。
func (a *App) reportUILocale(string) {}
