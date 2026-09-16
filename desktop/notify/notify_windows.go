//go:build windows

package notify

import (
	"context"
	"fmt"
	"os"
	"sync"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
)

// appID 是 AppUserModelID。Windows 用它决定通知归属哪个应用、
// 在操作中心里显示什么名字, 以及点击时唤起谁。
const appID = "net.czl.mail"

// appGUID 用于把 COM 激活回调路由回本进程。
//
// 必须是一个固定不变的值: Windows 会把它写进注册表, 每次构建换一个新 GUID 会
// 在系统里堆下一串死条目, 且旧通知的点击将无处可去。
const appGUID = "{8F3A2C41-6D5E-4B7A-9C18-2E4D7A9B0C31}"

type windowsNotifier struct {
	mu       sync.Mutex
	onAction func(string)
}

// New 构造平台通知器并完成注册。
//
// Windows 上不注册 AppUserModelID 的话 toast 根本不会显示 —— 不是报错,
// 是静默地什么都不发生, 这是最容易踩进去的坑。
func New() (Notifier, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("4001 locate executable: %w", err)
	}

	if err := toast.SetAppData(toast.AppData{
		AppID: appID,
		GUID:  appGUID,
		// 应用没在运行时, Windows 靠这个路径把它拉起来处理点击。
		ActivationExe: exe,
	}); err != nil {
		return nil, fmt.Errorf("4002 register notification app id: %w", err)
	}

	n := &windowsNotifier{}
	toast.SetActivationCallback(func(args string, _ []toast.UserData) {
		n.mu.Lock()
		fn := n.onAction
		n.mu.Unlock()
		if fn != nil && args != "" {
			fn(args)
		}
	})
	return n, nil
}

func (n *windowsNotifier) Notify(_ context.Context, msg Notification) error {
	notification := toast.Notification{
		AppID:               appID,
		Title:               msg.Title,
		Body:                msg.Body,
		ActivationType:      toast.Foreground,
		ActivationArguments: msg.ActionID,
		Duration:            toast.Short,
	}
	if err := notification.Push(); err != nil {
		return fmt.Errorf("4003 push notification: %w", err)
	}
	return nil
}

func (n *windowsNotifier) OnActivated(fn func(string)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onAction = fn
}

func (n *windowsNotifier) Close() error { return nil }
