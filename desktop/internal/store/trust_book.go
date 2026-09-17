package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// TrustedSendersBook 是 Bulwark 存放信任发件人的通讯录名。
//
// 信任名单存进服务端通讯录而不只是本地库, 换设备、换客户端(包括 Bulwark 网页端)都能共用。
// 名称沿用 Bulwark 的约定, 两边互通。
const TrustedSendersBook = "Trusted Senders"

// inTrustedBook 检查地址是否在任一账号的信任通讯录里。
func (s *Store) inTrustedBook(ctx context.Context, addr string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM contacts c
		JOIN address_books b ON b.account_id = c.account_id AND b.name = ?
		   AND EXISTS (SELECT 1 FROM json_each(c.address_book_ids) WHERE value = b.id),
		     json_each(c.emails_json) e
		WHERE LOWER(json_extract(e.value, '$.email')) = ?`,
		TrustedSendersBook, addr,
	).Scan(&n)
	if err != nil {
		return false, wrap(CodeQuery, "check trusted book", err)
	}
	return n > 0, nil
}

// TrustedBookEntry 是信任通讯录里的一张卡片。
type TrustedBookEntry struct {
	AccountID string
	CardID    string
	Address   string
}

// TrustedBookEntries 列出信任通讯录里的全部地址。address 非空时只取该地址。
func (s *Store) TrustedBookEntries(ctx context.Context, address string) ([]TrustedBookEntry, error) {
	query := `
		SELECT c.account_id, c.id, LOWER(json_extract(e.value, '$.email'))
		FROM contacts c
		JOIN address_books b ON b.account_id = c.account_id AND b.name = ?
		   AND EXISTS (SELECT 1 FROM json_each(c.address_book_ids) WHERE value = b.id),
		     json_each(c.emails_json) e
		WHERE json_extract(e.value, '$.email') IS NOT NULL`
	args := []any{TrustedSendersBook}
	if address != "" {
		query += ` AND LOWER(json_extract(e.value, '$.email')) = ?`
		args = append(args, NormalizeAddress(address))
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY 3`, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "list trusted book", err)
	}
	defer rows.Close()

	var out []TrustedBookEntry
	for rows.Next() {
		var e TrustedBookEntry
		if err := rows.Scan(&e.AccountID, &e.CardID, &e.Address); err != nil {
			return nil, wrap(CodeQuery, "scan trusted book", err)
		}
		out = append(out, e)
	}
	return out, wrap(CodeQuery, "iterate trusted book", rows.Err())
}

// AddressBookByName 按名称找通讯录。
func (s *Store) AddressBookByName(ctx context.Context, accountID, name string) (*AddressBook, error) {
	books, err := s.AddressBooks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range books {
		if books[i].AccountID == accountID && strings.EqualFold(books[i].Name, name) {
			return &books[i], nil
		}
	}
	return nil, ErrNotFound
}

// EmailWithAccount 是跨账号查询的邮件摘要。
type EmailWithAccount struct {
	AccountID string `json:"accountId"`
	EmailSummary
}

// RecentEmailsWith 返回与某地址往来的最近邮件(发件人或收件人含该地址)。
func (s *Store) RecentEmailsWith(ctx context.Context, address string, limit int) ([]EmailWithAccount, error) {
	addr := NormalizeAddress(address)
	if addr == "" {
		return nil, nil
	}
	// 地址在 JSON 里以 "email":"x" 出现, 带上引号避免子串误中。
	like := `%"email":"` + escapeLike(addr) + `"%`
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.account_id, e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+`
		FROM emails e
		WHERE LOWER(e.from_json) LIKE ? ESCAPE '\' OR LOWER(e.to_json) LIKE ? ESCAPE '\' OR LOWER(e.cc_json) LIKE ? ESCAPE '\'
		ORDER BY e.received_at DESC
		LIMIT ?`, like, like, like, limit)
	if err != nil {
		return nil, wrap(CodeQuery, "recent emails with", err)
	}
	defer rows.Close()

	var out []EmailWithAccount
	for rows.Next() {
		var r EmailWithAccount
		var fromJSON string
		var receivedAt int64
		var hasAttachment, unread, flagged, draft, pinned int
		if err := rows.Scan(&r.AccountID, &r.ID, &r.ThreadID, &r.Subject, &fromJSON, &receivedAt,
			&r.Preview, &hasAttachment, &r.Size, &unread, &flagged, &draft, &pinned); err != nil {
			return nil, wrap(CodeQuery, "scan recent email", err)
		}
		_ = json.Unmarshal([]byte(fromJSON), &r.From)
		r.ReceivedAt = time.Unix(receivedAt, 0)
		r.HasAttachment, r.IsUnread, r.IsFlagged, r.IsDraft = hasAttachment != 0, unread != 0, flagged != 0, draft != 0
		r.IsPinned = pinned != 0
		out = append(out, r)
	}
	return out, wrap(CodeQuery, "iterate recent emails", rows.Err())
}

// SearchFileNodes 按名称检索文件。
func (s *Store) SearchFileNodes(ctx context.Context, accountID, query string, limit int) ([]FileNode, error) {
	return s.scanFileNodes(ctx, `SELECT `+fileNodeColumns+` FROM file_nodes
		WHERE account_id = ? AND LOWER(name) LIKE ? ESCAPE '\'
		ORDER BY type = 'file', name COLLATE NOCASE LIMIT ?`,
		accountID, "%"+escapeLike(strings.ToLower(query))+"%", limit)
}
