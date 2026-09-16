package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/update"
)

// 在线更新。启动后与之后每 12 小时静默检查一次, 有新版本时通知界面; 安装由用户确认。

const (
	// EventUpdateAvailable 发现新版本, 负载为 UpdateInfo。
	EventUpdateAvailable = "update:available"
	// EventUpdateProgress 下载进度, 负载为 {done, total}。
	EventUpdateProgress = "update:progress"

	updateInterval = 12 * time.Hour
)

// UpdateInfo 是检查结果。
type UpdateInfo struct {
	Current   string          `json:"current"`
	Available bool            `json:"available"`
	Release   *update.Release `json:"release"`
	// Message 在没有可用版本或检查失败时说明原因。
	Message string `json:"message"`
}

var updateMu sync.Mutex

// CheckForUpdate 立即检查更新。
func (a *App) CheckForUpdate() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	info := UpdateInfo{Current: version}
	rel, err := update.Latest(ctx)
	if st, serr := a.currentStore(); serr == nil {
		_ = st.SetStringSetting(a.ctx, store.SettingUpdateCheckedAt, strconv.FormatInt(time.Now().Unix(), 10))
	}
	switch {
	case errors.Is(err, update.ErrNoRelease):
		info.Message = "暂无发布版本"
		return info, nil
	case err != nil:
		return info, fmt.Errorf("2150 %w", err)
	}
	info.Release = rel
	info.Available = update.Newer(rel.Version, version)
	if !info.Available {
		info.Message = "已是最新版本"
	}
	return info, nil
}

// InstallUpdate 下载并校验最新安装包, 启动安装程序后退出应用。
// 安装程序以静默模式运行, 结束后会自动重新打开 CZL Mail。
func (a *App) InstallUpdate() error {
	if !updateMu.TryLock() {
		return errors.New("2151 an update is already in progress")
	}
	defer updateMu.Unlock()

	info, err := a.CheckForUpdate()
	if err != nil {
		return err
	}
	if !info.Available {
		return errors.New("2152 no newer version available")
	}

	path, err := update.Download(a.ctx, info.Release, func(done, total int64) {
		a.emit(EventUpdateProgress, map[string]int64{"done": done, "total": total})
	})
	if err != nil {
		return fmt.Errorf("2153 %w", err)
	}

	if runtime.GOOS != "windows" {
		// macOS 的包是 .app 压缩包, 无法原地静默替换, 交给用户在访达里完成。
		return revealPath(path)
	}

	if err := exec.Command(path, "/S").Start(); err != nil {
		return fmt.Errorf("2154 start installer: %w", err)
	}
	wruntime.Quit(a.ctx)
	return nil
}

// runUpdateChecks 周期性检查更新。开发版本(version 为 dev)不检查。
func (a *App) runUpdateChecks(ctx context.Context) {
	if _, ok := parseVersion(version); !ok {
		return
	}
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if info, err := a.CheckForUpdate(); err == nil && info.Available {
			a.emit(EventUpdateAvailable, info)
		}
		timer.Reset(updateInterval)
	}
}

func parseVersion(v string) (string, bool) {
	return v, update.Newer("v999.0.0", v)
}
