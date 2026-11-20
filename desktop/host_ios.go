package main

import (
	"context"
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

// reportUILocale 把界面语言与推送要用的几句译文交给原生壳存进 App Group: 通知扩展 (另一个进程, 不带内核)
// 读不到语言文件, 直接用这里翻好的文字。more 里的 {0} 由扩展替换成多出来的封数。
func (a *App) reportUILocale(locale string) {
	texts := map[string]string{
		"locale":    locale,
		"newMail":   catalog.T(locale, "新邮件"),
		"noSubject": catalog.T(locale, "(无主题)"),
		"more":      catalog.T(locale, "另有 {0} 封新邮件"),
	}
	go func() {
		if _, err := hostRequest(context.Background(), "uiLocale", texts); err != nil {
			a.log.Debug("report ui locale", "err", err)
		}
	}()
}
