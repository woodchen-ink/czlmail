package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// platformInit 在内核启动前注册平台专属的原生实现。iOS 的密钥库直接调 Security 框架, 无需注册。
func platformInit() {}

func (a *App) openDirectoryDialog(d fileDialog) (string, error) {
	paths, err := a.hostOpenFiles(d, false, true)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// saveDir 是「文稿」目录: Info.plist 开了文件共享, 文件 App 里可见。
func (a *App) saveDir() (string, error) {
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

// installPackage 在 iOS 上不会被调用: 在线更新只在桌面端与 Android 运行。
func (a *App) installPackage(string) error {
	return fmt.Errorf("2154 self-update is not supported on iOS")
}
