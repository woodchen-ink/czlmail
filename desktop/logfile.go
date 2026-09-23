package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// maxLogBytes 超过后启动时把日志轮转为 .old, 只保留一份旧日志。
const maxLogBytes = 5 << 20

// newLogger 同时写 stderr 与 %APPDATA%\czlmail\czlmail.log。
// 桌面程序没有控制台, 出问题时用户只能把这个文件发过来。日志里不记录凭据与邮件内容。
func newLogger() *slog.Logger {
	var out io.Writer = os.Stderr
	if dir, err := os.UserConfigDir(); err == nil {
		path := filepath.Join(dir, config.DirName(), "czlmail.log")
		if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
			_ = os.Rename(path, path+".old")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				out = io.MultiWriter(os.Stderr, f)
			}
		}
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
