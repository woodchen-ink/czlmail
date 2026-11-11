//go:build ios

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// iOS 的宿主能力由原生壳实现, 经 hostRequest 往返。请求类型 (kind) 与 iOS 原生壳里的 Swift 处理一一对应, 改动两边一起改。

// hostOpenFiles 让原生壳弹文件选择器。原生壳把选中的文件复制进本程序沙箱后返回路径 ——
// 选择器给的是安全作用域 URL, Go 这边直接读不了。
func (a *App) hostOpenFiles(d fileDialog, multiple, directory bool) ([]string, error) {
	raw, err := hostRequest(a.ctx, "pickFiles", map[string]any{
		"title":     d.Title,
		"filters":   d.Filters,
		"multiple":  multiple,
		"directory": directory,
	})
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil, fmt.Errorf("2263 decode picked files: %w", err)
	}
	return paths, nil
}

func (a *App) openFileDialog(d fileDialog) (string, error) {
	paths, err := a.hostOpenFiles(d, false, false)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func (a *App) openFilesDialog(d fileDialog) ([]string, error) {
	return a.hostOpenFiles(d, true, false)
}

func (a *App) openDirectoryDialog(d fileDialog) (string, error) {
	paths, err := a.hostOpenFiles(d, false, true)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// saveFileDialog 在 iOS 上不弹对话框: 直接落到本程序的「文稿」目录 (文件 App 里可见),
// 重名时加序号。调用方写完文件后照常返回路径, 界面据此提示保存位置或弹分享面板。
func (a *App) saveFileDialog(d fileDialog) (string, error) {
	docs, err := documentsDir()
	if err != nil {
		return "", err
	}
	name := d.DefaultFilename
	if name == "" {
		name = "untitled"
	}
	return uniquePath(filepath.Join(docs, filepath.Base(name))), nil
}

func documentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("2264 locate documents dir: %w", err)
	}
	dir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("2264 create documents dir: %w", err)
	}
	return dir, nil
}

// uniquePath 在 path 已存在时返回 "名 (2).扩展名" 这类不冲突的路径。
func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		p := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}

func (a *App) openURL(url string) {
	go func() {
		if _, err := hostRequest(a.ctx, "openURL", map[string]string{"url": url}); err != nil {
			a.log.Warn("open url", "err", err)
		}
	}()
}

// quit 在 iOS 上不做事: 系统不允许程序自行退出。
func (a *App) quit() {}

func (a *App) emit(name string, data ...any) {
	if data == nil {
		data = []any{}
	}
	args, err := json.Marshal(data)
	if err != nil {
		a.log.Error("encode event", "name", name, "err", err)
		return
	}
	nativeEmit(name, args)
}

// 托盘在 iOS 上对应应用图标角标。
const hideOnClose = false

func (a *App) startTray() {}

func (a *App) stopTray() {}

func (a *App) setTrayUnread(n int) {
	go func() {
		if _, err := hostRequest(a.ctx, "badge", map[string]int{"count": n}); err != nil {
			a.log.Debug("set badge", "err", err)
		}
	}()
}

func (a *App) showWindow() {}
