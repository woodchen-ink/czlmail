//go:build !ios && !android

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
)

// 记住主窗口的位置、大小与是否最大化, 下次启动时还原。
//
// 存在数据目录的 window.json 而不是 config.json: 这是这台机器、这块屏幕的事,
// 与账号无关, 「退出登录」也不该把它清掉。
//
// 每隔几秒比对一次、有变化才写盘, 而不是只在退出时保存: 关机、注销时
// 托盘里的程序是被系统直接结束的, 走不到退出流程。

const windowStateFile = "window.json"

// windowCheckInterval 是比对窗口位置的间隔。读一次位置只是一个系统调用, 没变化不写盘。
const windowCheckInterval = 5 * time.Second

type windowState struct {
	// Width / Height 是窗口未最大化时的大小(与 options.App 相同的逻辑像素)。
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Maximized 为 true 时下次启动直接最大化。
	Maximized bool `json:"maximized,omitempty"`
	// Normal 是 Windows 上「向下还原」时的外框, 含位置。其他平台没有, 只还原大小。
	Normal *platform.WindowRect `json:"normal,omitempty"`
}

func (s windowState) equal(o windowState) bool {
	if s.Width != o.Width || s.Height != o.Height || s.Maximized != o.Maximized {
		return false
	}
	if s.Normal == nil || o.Normal == nil {
		return s.Normal == o.Normal
	}
	return *s.Normal == *o.Normal
}

type windowMemory struct {
	mu    sync.Mutex
	state windowState
	log   *slog.Logger
	// seen 表示窗口本次运行中已经显示过。开机自启时窗口一直藏在托盘里,
	// 还没按上次的状态最大化, 这时读到的「未最大化」不能覆盖保存的状态。
	seen atomic.Bool
}

// mainWindow 只有一个: 单实例锁保证每个进程一个主窗口。
var mainWindow = &windowMemory{}

func windowStatePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, windowStateFile), nil
}

// load 读取上次保存的状态。文件不存在或损坏时返回零值, 窗口按默认大小居中。
func (m *windowMemory) load(log *slog.Logger) windowState {
	m.log = log
	path, err := windowStatePath()
	if err != nil {
		return windowState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return windowState{}
	}
	var s windowState
	if err := json.Unmarshal(data, &s); err != nil {
		log.Warn("parse window state", "err", err)
		return windowState{}
	}
	m.state = s
	return s
}

// startOptions 把保存的大小与最大化状态填进启动参数。位置要等窗口创建后再设(restore)。
func (s windowState) startOptions(o *options.App) {
	// 太小的值(旧版本、手改的文件)交给最小尺寸兜底之前先丢掉, 免得窗口一启动就缩成一团。
	if s.Width >= o.MinWidth && s.Height >= o.MinHeight {
		o.Width, o.Height = s.Width, s.Height
	}
	if s.Maximized {
		o.WindowStartState = options.Maximised
	}
}

// restore 在窗口创建后、显示前把它挪回上次的位置, 然后开始跟踪变化。
// Wails 要等页面加载完才显示窗口, OnStartup 早于此, 所以用户看不到窗口跳动。
func (m *windowMemory) restore(ctx context.Context, startHidden bool) {
	m.seen.Store(!startHidden)
	m.mu.Lock()
	normal := m.state.Normal
	m.mu.Unlock()
	if normal != nil {
		// 上次的位置在另一块缩放比例不同的屏幕上时, 第一次挪过去会触发 DPI 变化,
		// 系统按新比例改掉窗口大小; 第二次窗口已在那块屏幕上, 大小就对了。
		if platform.PlaceMainWindow(*normal) {
			platform.PlaceMainWindow(*normal)
		}
	}
	go m.track(ctx)
}

// markSeen 在窗口从托盘被唤出时调用。
func (m *windowMemory) markSeen() { m.seen.Store(true) }

func (m *windowMemory) track(ctx context.Context) {
	t := time.NewTicker(windowCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.save(ctx, false)
		}
	}
}

// save 读取窗口当前状态, 与上次保存的不同才写盘。
// final 用于退出前的最后一次保存: 这时只用系统接口, 不再调 Wails 运行时
// (macOS 上那些调用要派发到主线程, 而退出回调本身就在主线程上)。
func (m *windowMemory) save(ctx context.Context, final bool) {
	if !m.seen.Load() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.state
	rect, maximized, native := platform.MainWindowPlacement()
	if native {
		next.Normal, next.Maximized = &rect, maximized
	} else if final {
		return
	} else {
		next.Maximized = wruntime.WindowIsMaximised(ctx)
	}
	// 最大化、最小化时窗口大小不是「向下还原」后的大小, 只在普通状态下更新。
	if !final && wruntime.WindowIsNormal(ctx) {
		next.Width, next.Height = wruntime.WindowGetSize(ctx)
	}
	if next.equal(m.state) {
		return
	}

	path, err := windowStatePath()
	if err == nil {
		err = writeFileAtomic(path, next)
	}
	if err != nil {
		if m.log != nil {
			m.log.Warn("save window state", "err", err)
		}
		return
	}
	m.state = next
}

func writeFileAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
