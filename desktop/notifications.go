package main

import (
	"fmt"
	"strings"

	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// notifyMax 限制单次同步弹出的通知条数。
//
// 首次同步或长时间离线后重连会一次性涌入成百上千封, 逐条弹通知会淹没操作中心,
// 超出的部分合并成一条汇总。
const notifyMax = 5

// initNotifier 准备系统通知。失败只记日志 —— 收不到通知远不如收不到邮件严重。
func (a *App) initNotifier() {
	n, err := notify.New()
	if err != nil {
		a.log.Warn("system notifications unavailable", "err", err)
		return
	}

	n.OnActivated(func(actionID string) {
		// actionID 形如 "accountID/emailID", 交给前端去定位并展开那封邮件。
		// 窗口可能正缩在托盘里。
		a.showWindow()
		if ref, ok := strings.CutPrefix(actionID, "event:"); ok {
			a.emit(EventOpenEvent, ref)
			return
		}
		a.emit(EventOpenEmail, actionID)
	})

	a.mu.Lock()
	a.notifier = n
	a.mu.Unlock()
}

func (a *App) notifyNewMail(notices []newMailNotice) {
	a.mu.RLock()
	n := a.notifier
	ctx := a.ctx
	a.mu.RUnlock()

	if n == nil || ctx == nil {
		return
	}

	if len(notices) > notifyMax {
		_ = n.Notify(ctx, notify.Notification{
			Title: "新邮件",
			Body:  fmt.Sprintf("收到 %d 封新邮件", len(notices)),
		})
		return
	}

	for _, notice := range notices {
		_ = n.Notify(ctx, notify.Notification{
			Title:    notice.From,
			Body:     notificationBody(notice),
			ActionID: notice.AccountID + "/" + notice.ID,
		})
	}
}

// notificationBody 用主题加摘要, 主题为空时退回摘要。
func notificationBody(notice newMailNotice) string {
	subject := strings.TrimSpace(notice.Subject)
	if subject == "" {
		subject = "(无主题)"
	}

	preview := strings.TrimSpace(notice.Preview)
	if preview == "" {
		return subject
	}
	return subject + "\n" + preview
}
