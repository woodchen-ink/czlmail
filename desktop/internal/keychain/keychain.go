// Package keychain 读写系统密钥库里的凭据。
//
// 桌面端走 go-keyring (Windows 凭据管理器 / macOS 钥匙串 / Secret Service);
// iOS 上 go-keyring 会去执行 /usr/bin/security, 沙箱里没有这个程序, 改为直接调 Security 框架;
// Android 交给原生壳 (Android Keystore 加密存放), 见 keychain_android.go。
// 调用方只依赖本包, 不直接 import go-keyring。
package keychain

import "errors"

// ErrNotFound 表示条目不存在。读取时调用方据此区分"没存过"与"密钥库不可用"。
var ErrNotFound = errors.New("secret not found in keychain")

// Get 读取 service 下 user 条目的内容。
func Get(service, user string) (string, error) { return get(service, user) }

// Set 写入或覆盖条目。
func Set(service, user, secret string) error { return set(service, user, secret) }

// Delete 删除条目; 条目不存在时返回 ErrNotFound。
func Delete(service, user string) error { return del(service, user) }
