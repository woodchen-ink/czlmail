// Package config 管理可明文落盘的连接配置、数据目录, 以及系统密钥库里的凭据。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

// KeyringService 是凭据在系统密钥库里的服务名。
// Windows 落凭据管理器, macOS 落 Keychain, Linux 落 Secret Service。
const KeyringService = "czlmail"

// Config 是可以明文落盘的部分。
//
// 不含任何凭据: 访问令牌与刷新令牌只进系统密钥库。client_id 不是机密 ——
// 本应用注册为公开客户端, 它本来就会出现在浏览器地址栏里。
type Config struct {
	// ServerHost 是邮件服务器地址, 如 mail.example.net
	ServerHost string `json:"serverHost"`

	// AuthMethod 决定凭据形态与恢复会话的方式: AuthPassword 或 AuthOAuth。
	AuthMethod string `json:"authMethod"`

	// Issuer 与 SessionEndpoint 由发现流程填充, 缓存下来避免每次启动重新发现。
	// Issuer 指向授权服务器, SessionEndpoint 指向邮件服务器, 二者可能不同。
	Issuer          string `json:"issuer"`
	SessionEndpoint string `json:"sessionEndpoint"`
	// ClientID 与 RedirectURI 来自动态注册, 成对缓存: 端口变了就得重新注册。
	ClientID    string   `json:"clientId"`
	RedirectURI string   `json:"redirectUri"`
	Scopes      []string `json:"scopes"`
	// Username 在会话建立后由服务器给出, 仅用于界面显示与密钥库的条目名。
	Username string `json:"username"`
}

const (
	// AuthPassword 用邮箱 + 应用专用密码走 Basic 认证。任何 JMAP 服务器都支持,
	// 且在账号走外部 SSO 的部署上是唯一不依赖服务端额外配置的方式。
	AuthPassword = "password"
	// AuthOAuth 在浏览器中走授权码流程, 需要服务器支持动态客户端注册。
	AuthOAuth = "oauth"
)

// Configured 报告是否已完成过一次登录。
func (c Config) Configured() bool {
	if c.SessionEndpoint == "" {
		return false
	}
	if c.AuthMethod == AuthPassword {
		return c.Username != ""
	}
	return c.Issuer != "" && c.ClientID != ""
}

// InstanceID 是单实例锁与 macOS LaunchAgent 的标识。开发调试时设置 CZLMAIL_INSTANCE 可以与已安装的正式版同时运行。
func InstanceID() string {
	if v := os.Getenv("CZLMAIL_INSTANCE"); v != "" {
		return "net.czl.mail." + v
	}
	return "net.czl.mail"
}

// DirName 是 %APPDATA% 下的目录名。设置了 CZLMAIL_INSTANCE 的开发实例用独立目录,
// 缓存库、配置、日志、MCP 连接信息都与正式版分开, 测试不会影响日常使用的数据。
func DirName() string {
	if v := os.Getenv("CZLMAIL_INSTANCE"); v != "" {
		return "czlmail-" + v
	}
	return "czlmail"
}

// Dir 返回数据目录, 不存在时创建。
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("2010 locate user config dir: %w", err)
	}
	dir := filepath.Join(base, DirName())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("2011 create data dir: %w", err)
	}
	return dir, nil
}

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// DatabasePath 返回缓存数据库的位置。
func DatabasePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache.db"), nil
}

// Load 读取配置。首次启动时文件不存在, 返回零值且 err 为 nil,
// 由界面引导用户登录, 而不是在这里失败。
func Load() (Config, error) {
	var cfg Config

	path, err := filePath()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("2012 read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("2013 parse config: %w", err)
	}
	return cfg, nil
}

// Save 写入配置。权限 0600: 虽不含凭据, 但暴露了邮箱地址与服务器。
func Save(cfg Config) error {
	path, err := filePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("2014 encode config: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// TokenKey 是 OAuth 令牌在密钥库里的条目名, 让同一台机器上的多个服务器各自持有独立条目。
func TokenKey(issuer string) string {
	return "oauth:" + issuer
}

// LoadToken 从系统密钥库取 OAuth 令牌。未登录时返回 nil 且 err 为 nil。
//
// 刻意不提供"存到配置文件"的回落路径: 有回落就一定会在密钥库不可用时被静默使用,
// 而一个明文躺在磁盘上的长期刷新令牌, 比一次明确的登录失败危险得多。
func LoadToken(issuer string) (*oauth2.Token, error) {
	raw, err := keyring.Get(KeyringService, TokenKey(issuer))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("2015 read credential from system keyring: %w", err)
	}

	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, fmt.Errorf("2016 parse stored credential: %w", err)
	}
	return &tok, nil
}

// SaveToken 把令牌写入系统密钥库。刷新令牌轮换时也走这里。
func SaveToken(issuer string, tok *oauth2.Token) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("2017 encode credential: %w", err)
	}
	if err := keyring.Set(KeyringService, TokenKey(issuer), string(raw)); err != nil {
		return fmt.Errorf("2018 write credential to system keyring: %w", err)
	}
	return nil
}

// DeleteToken 清除已保存的令牌, 用于退出登录。
func DeleteToken(issuer string) error {
	err := keyring.Delete(KeyringService, TokenKey(issuer))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("2019 delete credential from system keyring: %w", err)
	}
	return nil
}

// PasswordKey 是应用专用密码的条目名, 按会话地址与用户名区分条目, 同一台机器上可以并存多个邮箱。
func PasswordKey(sessionEndpoint, username string) string {
	return "password:" + sessionEndpoint + "|" + username
}

// LoadPassword 从系统密钥库取应用专用密码。不存在时返回空串且 err 为 nil。
func LoadPassword(sessionEndpoint, username string) (string, error) {
	v, err := keyring.Get(KeyringService, PasswordKey(sessionEndpoint, username))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("2015 read credential from system keyring: %w", err)
	}
	return v, nil
}

// SavePassword 把应用专用密码写入系统密钥库。与令牌一样没有明文回落路径。
func SavePassword(sessionEndpoint, username, password string) error {
	if err := keyring.Set(KeyringService, PasswordKey(sessionEndpoint, username), password); err != nil {
		return fmt.Errorf("2018 write credential to system keyring: %w", err)
	}
	return nil
}

// DeletePassword 清除已保存的应用专用密码。
func DeletePassword(sessionEndpoint, username string) error {
	err := keyring.Delete(KeyringService, PasswordKey(sessionEndpoint, username))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("2019 delete credential from system keyring: %w", err)
	}
	return nil
}
