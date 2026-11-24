package syncer

import (
	"context"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// FillCorrespondencePage 让服务端按 store.EmailsWith 的同一条件(地址 + 方向)取第 [position, position+limit) 封,
// 本地没有的补元数据进缓存; 返回服务端的总数。不写 state、不拿同步锁。
// 服务端的 from/to 条件是子串匹配, 可能多取几封, 本地查询按完整地址再筛一遍。
func (s *Syncer) FillCorrespondencePage(ctx context.Context, accountID string, addresses []string, direction string, position, limit int) (int, error) {
	var conds []email.Filter
	for _, raw := range addresses {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			continue
		}
		if direction != store.DirectionTo {
			conds = append(conds, &email.FilterCondition{From: addr})
		}
		if direction != store.DirectionFrom {
			conds = append(conds, &email.FilterCondition{To: addr}, &email.FilterCondition{Cc: addr}, &email.FilterCondition{Bcc: addr})
		}
	}
	if len(conds) == 0 {
		return 0, nil
	}
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Query{
		Account:        s.server(accountID),
		Filter:         &email.FilterOperator{Operator: jmap.OperatorOR, Conditions: conds},
		Sort:           []*email.SortComparator{{Property: "receivedAt", IsAscending: false}},
		Position:       int64(position),
		Limit:          uint64(limit),
		CalculateTotal: true,
	})
	resp, err := s.do(req)
	if err != nil {
		return 0, err
	}
	q, ok := resp.Responses[0].Args.(*email.QueryResponse)
	if !ok {
		return 0, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/query"}
	}
	if _, err := s.cacheMissing(ctx, accountID, q.IDs); err != nil {
		return 0, err
	}
	return int(q.Total), nil
}
