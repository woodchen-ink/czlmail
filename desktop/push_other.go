//go:build !ios

package main

import (
	"context"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 新邮件推送只在 iOS 上运行 (见 push_ios.go、push_relay.go): 桌面端常驻托盘收信, Android 由前台服务保持同步。
var pushEnabled = false

func (a *App) runPush(context.Context, string, *syncer.Syncer, *store.Store) {}
