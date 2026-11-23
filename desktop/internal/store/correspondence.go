package store

import (
	"context"
	"strings"
)

// 与某人的往来邮件按方向筛选。
const (
	DirectionAll  = ""     // 对方是发件人或收件人
	DirectionFrom = "from" // 对方发来的: 发件人是对方
	DirectionTo   = "to"   // 发给对方的: 收件人 / 抄送 / 密送里有对方
)

// directionColumns 是各方向要匹配的地址列。
func directionColumns(direction string) []string {
	switch direction {
	case DirectionFrom:
		return []string{"from_json"}
	case DirectionTo:
		return []string{"to_json", "cc_json", "bcc_json"}
	default:
		return []string{"from_json", "to_json", "cc_json", "bcc_json"}
	}
}

// correspondenceWhere 生成「地址列里出现任一地址」的条件。
// 地址在 JSON 里以 "email":"x" 出现, 带上引号避免子串误中。
func correspondenceWhere(addresses []string, direction string) (string, []any) {
	var conds []string
	var args []any
	for _, raw := range addresses {
		addr := NormalizeAddress(raw)
		if addr == "" {
			continue
		}
		like := `%"email":"` + escapeLike(addr) + `"%`
		for _, col := range directionColumns(direction) {
			conds = append(conds, "LOWER(e."+col+`) LIKE ? ESCAPE '\'`)
			args = append(args, like)
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "(" + strings.Join(conds, " OR ") + ")", args
}

// EmailsWith 分页列出与这些地址往来的邮件, 跨全部账号按收件时间倒序; 同时返回本地命中的总数。
func (s *Store) EmailsWith(ctx context.Context, addresses []string, direction string, limit, offset int) ([]EmailSummary, int, error) {
	where, args := correspondenceWhere(addresses, direction)
	if where == "" {
		return nil, 0, nil
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emails e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, wrap(CodeQuery, "count emails with", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+summaryColumns+`
		FROM emails e
		WHERE `+where+`
		ORDER BY e.received_at DESC, e.id
		LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, wrap(CodeQuery, "emails with", err)
	}
	defer rows.Close()
	list, err := scanSummaries(rows)
	return list, total, err
}

// RecentEmailsWith 返回与某地址往来的最近邮件(发件人或收件人含该地址), 跨全部账号。
func (s *Store) RecentEmailsWith(ctx context.Context, address string, limit int) ([]EmailSummary, error) {
	list, _, err := s.EmailsWith(ctx, []string{address}, DirectionAll, limit, 0)
	return list, err
}
