//go:build darwin

package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// macOS 走 Wails 自带的 UNUserNotificationCenter 封装: 通知归属本应用(显示应用图标与名字,
// 在「系统设置 → 通知」里有独立条目), 点击能回调到这里以定位邮件。
//
// 前提是程序以 .app 包运行、Info.plist 里有 CFBundleIdentifier; 不满足时(比如直接运行
// 包内的二进制)退回 osascript —— 那条路径的通知挂在「脚本编辑器」名下, 点击也不会回到本应用,
// 只当兜底。
type darwinNotifier struct {
	ctx    context.Context
	native bool
	seq    atomic.Uint64

	mu       sync.Mutex
	onAction func(string)
}

// actionKey 是 ActionID 在通知 userInfo 里的键。
const actionKey = "action"

// defaultAction 是 Wails 对「点击通知本体」的动作标识(由 Apple 的默认标识换算而来)。
const defaultAction = "DEFAULT_ACTION"

// New 初始化通知中心。ctx 必须是 Wails OnStartup 收到的那个。
func New(ctx context.Context) (Notifier, error) {
	n := &darwinNotifier{ctx: ctx}
	if err := runtime.InitializeNotifications(ctx); err != nil {
		// 退回 osascript, 不算失败: 有通知总比没有好。
		return n, nil
	}
	n.native = true

	runtime.OnNotificationResponse(ctx, func(r runtime.NotificationResult) {
		if r.Error != nil || r.Response.ActionIdentifier != defaultAction {
			return
		}
		action, _ := r.Response.UserInfo[actionKey].(string)
		n.mu.Lock()
		fn := n.onAction
		n.mu.Unlock()
		if fn != nil {
			fn(action)
		}
	})

	// 首次运行时系统会弹窗询问是否允许通知, 用户可能半天不点, 不能挡住启动。
	// 已经允许或拒绝过的, 这一步立即返回, 不会再弹。
	go func() { _, _ = runtime.RequestNotificationAuthorization(ctx) }()
	return n, nil
}

func (n *darwinNotifier) Notify(ctx context.Context, msg Notification) error {
	if !n.native {
		return notifyScript(ctx, msg)
	}
	// 标识相同的通知会互相替换, 汇总类通知没有 ActionID, 用序号保证每条都留在通知中心。
	id := msg.ActionID
	if id == "" {
		id = "czlmail-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(n.seq.Add(1), 10)
	}
	err := runtime.SendNotification(n.ctx, runtime.NotificationOptions{
		ID:    id,
		Title: msg.Title,
		Body:  msg.Body,
		Data:  map[string]any{actionKey: msg.ActionID},
	})
	if err != nil {
		return fmt.Errorf("4010 display notification: %w", err)
	}
	return nil
}

func (n *darwinNotifier) OnActivated(fn func(string)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onAction = fn
}

func (n *darwinNotifier) Close() error { return nil }

// notifyScript 是没有 bundle 时的兜底, 拿不到点击回调。
func notifyScript(ctx context.Context, msg Notification) error {
	script := fmt.Sprintf(
		"display notification %s with title %s",
		quoteAppleScript(msg.Body),
		quoteAppleScript(msg.Title),
	)

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("4010 display notification: %w", err)
	}
	return nil
}

// quoteAppleScript 把字符串包成 AppleScript 字面量。
//
// 邮件的标题与发件人完全由外部输入控制, 直接拼进脚本等于把 AppleScript 执行权
// 交给发信人。反斜杠必须先于引号转义, 顺序反了会把已转义的引号再次破坏。
func quoteAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	// AppleScript 的字符串字面量里不能出现裸换行。
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
