package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 整夹拉取: 用户在文件夹上右键, 把服务端这个文件夹的全部邮件拉进本地,
// 之后离线翻阅与本地搜索都能覆盖到历史邮件。

// EventPullProgress 整夹拉取的进度, 负载为 PullStatus。
const EventPullProgress = "mail:pull-progress"

// PullStatus 是推给界面的拉取状态。Done 为真时 Error 为空表示成功。
type PullStatus struct {
	AccountID string `json:"accountId"`
	MailboxID string `json:"mailboxId"`
	syncer.PullProgress
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

var (
	pullsMu sync.Mutex
	pulls   = map[string]bool{}
)

// PullMailbox 在后台开始拉取, 立即返回。同一文件夹正在拉取时返回错误,
// 避免用户连点两次跑出两条互相重复下载的任务。
func (a *App) PullMailbox(accountID, mailboxID string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}

	key := accountID + "/" + mailboxID
	pullsMu.Lock()
	if pulls[key] {
		pullsMu.Unlock()
		return fmt.Errorf("2080 this folder is already being pulled")
	}
	pulls[key] = true
	pullsMu.Unlock()

	go func() {
		defer func() {
			pullsMu.Lock()
			delete(pulls, key)
			pullsMu.Unlock()
		}()

		status := PullStatus{AccountID: accountID, MailboxID: mailboxID}
		// 列表按批刷新: 每批都通知会让界面在几十秒里反复重载同一页。
		sinceRefresh := 0
		err := s.PullMailbox(context.WithoutCancel(a.ctx), accountID, mailboxID, func(p syncer.PullProgress) {
			sinceRefresh += p.Fetched - status.Fetched
			status.PullProgress = p
			a.emit(EventPullProgress, status)
			if sinceRefresh >= 2000 {
				sinceRefresh = 0
				a.emit(EventMailChanged, accountID)
			}
		})

		status.Done = true
		if err != nil {
			a.log.Error("pull mailbox", "account", accountID, "mailbox", mailboxID, "err", err)
			status.Error = err.Error()
		}
		a.emit(EventPullProgress, status)
		a.emit(EventMailChanged, accountID)
	}()
	return nil
}
