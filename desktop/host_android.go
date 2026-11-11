package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/keychain"
)

// platformInit 在内核启动前注册平台专属的原生实现。
//   - 凭据: Android 没有 go-keyring 能用的密钥库, 交给原生壳, 由 Android Keystore 里的密钥加密后存放
//   - OAuth: 授权页开在系统浏览器里, 回调后由浏览器跳 czlmail://signin 把本程序带回前台
func platformInit() {
	keychain.SetNative(androidSecrets{})
	auth.ReturnURL = "czlmail://signin"
}

// openDirectoryDialog 在 Android 上不支持: 系统的目录选择只给 content:// 树, Go 读不了里面的文件。
func (a *App) openDirectoryDialog(fileDialog) (string, error) {
	return "", errors.New("2270 choosing a folder is not supported on Android")
}

// saveDir 由原生壳给出: Android 11 起是公共「下载/CZL Mail」目录 (文件管理器里可见, 不需要存储权限),
// 更早的系统退回本程序的外部文件目录。
func (a *App) saveDir() (string, error) {
	raw, err := hostRequest(context.Background(), "saveDir", struct{}{})
	if err != nil {
		return "", fmt.Errorf("2264 locate save dir: %w", err)
	}
	var dir string
	if err := json.Unmarshal(raw, &dir); err != nil || dir == "" {
		return "", fmt.Errorf("2264 decode save dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("2264 create save dir: %w", err)
	}
	return dir, nil
}

// androidSecrets 经宿主请求读写原生壳的加密存储。
type androidSecrets struct{}

type secretReply struct {
	Found bool   `json:"found"`
	Value string `json:"value"`
}

func (androidSecrets) Get(service, user string) (string, error) {
	raw, err := hostRequest(context.Background(), "secretGet", map[string]string{"service": service, "user": user})
	if err != nil {
		return "", err
	}
	var r secretReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("2271 decode secret: %w", err)
	}
	if !r.Found {
		return "", keychain.ErrNotFound
	}
	return r.Value, nil
}

func (androidSecrets) Set(service, user, secret string) error {
	_, err := hostRequest(context.Background(), "secretSet", map[string]string{"service": service, "user": user, "value": secret})
	return err
}

func (androidSecrets) Delete(service, user string) error {
	raw, err := hostRequest(context.Background(), "secretDelete", map[string]string{"service": service, "user": user})
	if err != nil {
		return err
	}
	var r secretReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("2271 decode secret: %w", err)
	}
	if !r.Found {
		return keychain.ErrNotFound
	}
	return nil
}

// installPackage 把下载校验过的 APK 交给系统安装器。首次使用时系统会让用户允许本程序安装应用。
func (a *App) installPackage(path string) error {
	_, err := hostRequest(a.ctx, "installPackage", map[string]string{"path": path})
	return err
}
