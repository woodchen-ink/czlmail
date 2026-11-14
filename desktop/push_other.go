//go:build !ios

package main

import (
	"context"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 新邮件推送只在 iOS 上需要 (见 push_ios.go): 桌面端常驻托盘收信, Android 由前台服务保持同步。

func (a *App) primePush(context.Context, *store.Store) {}

func (a *App) runPush(context.Context, *syncer.Syncer, *store.Store) {}

func (a *App) pushCovers(string) bool { return false }

func (a *App) wakePush() {}

func (a *App) pushSettingsChanged() {}
