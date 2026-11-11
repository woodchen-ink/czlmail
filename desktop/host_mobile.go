//go:build ios || android

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 移动端的宿主能力由原生壳实现, 经 hostRequest 往返。请求类型 (kind) 与 iOS 壳的 Swift (私有仓库 czlmail-mobile)、
// Android 壳的 Kotlin (android/) 处理一一对应, 改动各端一起改。平台各自的部分在 host_ios.go / host_android.go。

const isMobile = true

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

// saveFileDialog 在移动端不弹对话框: 直接落到用户能找到的目录 (iOS 为「文稿」, 文件 App 里可见;
// Android 为「下载/CZL Mail」), 重名时加序号。调用方写完文件后照常返回路径, 界面据此提示保存位置。
func (a *App) saveFileDialog(d fileDialog) (string, error) {
	docs, err := a.saveDir()
	if err != nil {
		return "", err
	}
	name := d.DefaultFilename
	if name == "" {
		name = "untitled"
	}
	return uniquePath(filepath.Join(docs, filepath.Base(name))), nil
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

// quit 在移动端不做事: 程序的生命周期由系统管理。
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

// 托盘在移动端对应应用图标角标。
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
