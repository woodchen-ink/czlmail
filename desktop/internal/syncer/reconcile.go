package syncer

import (
	"context"
	"database/sql"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// reconcilePage 是每次 Email/query 取回的 id 数。只取 id, 单页几十 KB。
const reconcilePage = 5000

// ReconcileEmails 删除本地有、服务器上已不存在的邮件。
//
// 平时靠 Email/changes 的 destroyed 列表同步删除; 但离线太久、服务器已回收旧 state
// (cannotCalculateChanges)时只能重新引导, 引导只拉最新一批, 这期间在别处删掉的邮件
// 永远不会出现在任何增量里, 必须拿服务器的完整 id 集合对账。
// 服务器 id 没有取全(翻页中途出错或数量对不上)时不删任何东西。
func (s *Syncer) ReconcileEmails(ctx context.Context, accountID string) (int, error) {
	server := map[string]bool{}
	var total uint64
	for position := uint64(0); ; {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Query{
			Account:        jmap.ID(accountID),
			Position:       int64(position),
			Limit:          reconcilePage,
			CalculateTotal: true,
		})
		resp, err := s.do(req)
		if err != nil {
			return 0, err
		}
		got, ok := resp.Responses[0].Args.(*email.QueryResponse)
		if !ok {
			return 0, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/query"}
		}
		total = got.Total
		for _, id := range got.IDs {
			server[string(id)] = true
		}
		position += uint64(len(got.IDs))
		if len(got.IDs) == 0 || position >= total {
			break
		}
	}
	if uint64(len(server)) < total {
		s.log.Warn("reconcile skipped: incomplete server id list", "account", accountID, "got", len(server), "total", total)
		return 0, nil
	}

	local, err := s.store.LocalEmailIDs(ctx, accountID)
	if err != nil {
		return 0, err
	}
	var gone []string
	for _, id := range local {
		if !server[id] {
			gone = append(gone, id)
		}
	}
	if len(gone) == 0 {
		return 0, nil
	}
	if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.DeleteEmails(ctx, tx, accountID, gone)
	}); err != nil {
		return 0, err
	}
	s.log.Info("reconciled emails deleted on server", "account", accountID, "removed", len(gone))
	return len(gone), nil
}
