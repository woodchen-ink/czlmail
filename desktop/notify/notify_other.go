//go:build !windows && !darwin

package notify

import "context"

// 非 Windows / macOS 平台暂无实现。返回一个什么都不做的通知器而不是报错:
// 通知缺失不应妨碍邮件客户端本身可用。
type noopNotifier struct{}

func New() (Notifier, error) { return noopNotifier{}, nil }

func (noopNotifier) Notify(context.Context, Notification) error { return nil }
func (noopNotifier) OnActivated(func(string))                   {}
func (noopNotifier) Close() error                               { return nil }
