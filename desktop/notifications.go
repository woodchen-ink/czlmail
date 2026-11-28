package main

import (
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
	n, err := notify.New(a.ctx)
	if err != nil {
		a.log.Warn("system notifications unavailable", "err", err)
		return
	}

	n.OnActivated(func(actionID string) {
		// actionID 形如 "accountID/emailID", 交给前端去定位并展开那封邮件。
		// 窗口可能正缩在托盘里。
		a.showWindow()
		if actionID == signInActionID {
			// 窗口出来就够了: 重新登录的提示已经在界面上。
			return
		}
		if actionID == "update:" {
			a.emit(EventOpenUpdate, "")
			return
		}
		if ref, ok := strings.CutPrefix(actionID, "event:"); ok {
			a.emit(EventOpenEvent, ref)
			return
		}
		// 任务提醒同邮件走打开请求队列: 手机上点提醒常是冷启动, 直接发事件时界面还没加载, 会丢。
		if ref, ok := strings.CutPrefix(actionID, "task:"); ok {
			acc, id, found := strings.Cut(ref, "/")
			if !found || acc == "" || id == "" {
				return
			}
			a.opens.mu.Lock()
			a.opens.pending = append(a.opens.pending, OpenRequest{Kind: "task", AccountID: acc, ID: id})
			a.opens.mu.Unlock()
			a.emit(EventOpenRequest)
			return
		}
		// 邮件走与 czlmail:// 链接相同的打开队列: 移动端点通知常是冷启动, 这时界面还没加载,
		// 直接发事件没人收, 会停在默认的收件箱; 队列里的请求由界面加载完后自己来取。
		acc, id, ok := a.mailActionTarget(actionID)
		if !ok {
			return
		}
		a.opens.mu.Lock()
		a.opens.pending = append(a.opens.pending, OpenRequest{Kind: "email", AccountID: acc, ID: id})
		a.opens.mu.Unlock()
		a.emit(EventOpenRequest)
	})

	a.mu.Lock()
	a.notifier = n
	a.mu.Unlock()
}

// mailActionTarget 解析邮件通知的 actionID "<本机账号 id>/<邮件 id>", 返回账号与邮件 id。
// 同步弹的通知与 iOS 推送通知 (通知扩展按解得开的 auth 认出登录, 拼好带前缀的本机账号 id, 见 push_relay.go) 是同一格式;
// 附加登录的账号 id 带前缀 (s2~c), 前缀里没有 "/"。
func (a *App) mailActionTarget(actionID string) (accountID, emailID string, ok bool) {
	acc, id, found := strings.Cut(actionID, "/")
	if !found || acc == "" || id == "" {
		return "", "", false
	}
	return acc, id, true
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
			Title: a.t("新邮件"),
			Body:  a.t("收到 {0} 封新邮件", len(notices)),
		})
		return
	}

	for _, notice := range notices {
		_ = n.Notify(ctx, notify.Notification{
			Title:    notice.From,
			Body:     notificationBody(notice, a.t("(无主题)")),
			ActionID: notice.AccountID + "/" + notice.ID,
		})
	}
}

// notificationBody 用主题加摘要, 主题为空时退回摘要。
// noSubject 是按界面语言写好的「(无主题)」。
func notificationBody(notice newMailNotice, noSubject string) string {
	subject := strings.TrimSpace(notice.Subject)
	if subject == "" {
		subject = noSubject
	}

	preview := strings.TrimSpace(notice.Preview)
	if preview == "" {
		return subject
	}
	return subject + "\n" + preview
}
