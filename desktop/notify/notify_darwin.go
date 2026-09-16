//go:build darwin

package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// macOS 实现暂时走 osascript。
//
// 已知限制: 这条路径**拿不到点击回调**, 因此在 macOS 上点通知不会跳到对应邮件。
// 要做到那一步需要用 CGO 调 UNUserNotificationCenter, 且应用必须签名并打成
// .app bundle, 否则系统拒绝投递。在补上原生实现之前, macOS 只有通知展示。
type darwinNotifier struct {
	mu       sync.Mutex
	onAction func(string)
}

func New() (Notifier, error) {
	return &darwinNotifier{}, nil
}

func (n *darwinNotifier) Notify(ctx context.Context, msg Notification) error {
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

// OnActivated 在当前实现下不会被触发, 仍然保存回调, 以便换成原生实现时
// 调用方无需改动。
func (n *darwinNotifier) OnActivated(fn func(string)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onAction = fn
}

func (n *darwinNotifier) Close() error { return nil }

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
