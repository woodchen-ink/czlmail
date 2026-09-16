package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// AddressBook 是通讯录容器。
type AddressBook struct {
	AccountID    string          `json:"accountId"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	SortOrder    int64           `json:"sortOrder"`
	IsSubscribed bool            `json:"isSubscribed"`
	IsDefault    bool            `json:"isDefault"`
	MyRights     map[string]bool `json:"myRights"`
}

// ContactRow 是联系人落库的形态。
type ContactRow struct {
	ID             string
	AddressBookIDs []string
	UID            string
	Kind           string
	DisplayName    string
	SortName       string
	// Emails 按偏好排序。
	Emails       []ContactEmailRow
	Phones       []string
	Organization string
	Keywords     []string
	HasPhoto     bool
	Raw          string
	UpdatedAt    int64
}

// ContactEmailRow 与 isInAddressBook 的 $.email 路径对应。
type ContactEmailRow struct {
	Email string `json:"email"`
	Label string `json:"label,omitempty"`
}

// ContactSummary 是联系人列表的一行。
type ContactSummary struct {
	AccountID      string   `json:"accountId"`
	ID             string   `json:"id"`
	AddressBookIDs []string `json:"addressBookIds"`
	DisplayName    string   `json:"displayName"`
	Organization   string   `json:"organization"`
	Email          string   `json:"email"`
	Phone          string   `json:"phone"`
	Kind           string   `json:"kind"`
	Keywords       []string `json:"keywords"`
	HasPhoto       bool     `json:"hasPhoto"`
}

// Recipient 是收件人补全的候选。
type Recipient struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ReplaceAddressBooks 用全量列表替换某账号的通讯录。
func ReplaceAddressBooks(ctx context.Context, tx *sql.Tx, accountID string, list []AddressBook) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM address_books WHERE account_id = ?`, accountID); err != nil {
		return wrap(CodeQuery, "clear address books", err)
	}
	for _, b := range list {
		rights, _ := json.Marshal(b.MyRights)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO address_books (account_id, id, name, description, sort_order, is_subscribed, is_default, my_rights)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, b.ID, b.Name, b.Description, b.SortOrder,
			boolToInt(b.IsSubscribed), boolToInt(b.IsDefault), string(rights),
		); err != nil {
			return wrap(CodeQuery, "insert address book", err)
		}
	}
	return nil
}

// AddressBooks 列出全部账号的通讯录。
func (s *Store) AddressBooks(ctx context.Context) ([]AddressBook, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.account_id, b.id, b.name, b.description, b.sort_order, b.is_subscribed, b.is_default, b.my_rights
		FROM address_books b
		LEFT JOIN accounts a ON a.id = b.account_id
		ORDER BY COALESCE(a.is_personal, 0) DESC, b.account_id, b.is_default DESC, b.sort_order, b.name`)
	if err != nil {
		return nil, wrap(CodeQuery, "list address books", err)
	}
	defer rows.Close()

	var out []AddressBook
	for rows.Next() {
		var b AddressBook
		var subscribed, isDefault int
		var rights string
		if err := rows.Scan(&b.AccountID, &b.ID, &b.Name, &b.Description, &b.SortOrder, &subscribed, &isDefault, &rights); err != nil {
			return nil, wrap(CodeQuery, "scan address book", err)
		}
		b.IsSubscribed, b.IsDefault = subscribed != 0, isDefault != 0
		_ = json.Unmarshal([]byte(rights), &b.MyRights)
		out = append(out, b)
	}
	return out, wrap(CodeQuery, "iterate address books", rows.Err())
}

// UpsertContacts 写入联系人。
func UpsertContacts(ctx context.Context, tx *sql.Tx, accountID string, list []ContactRow) error {
	if len(list) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO contacts (account_id, id, address_book_ids, uid, kind, display_name, sort_name,
		                      emails_json, phones_json, organization, raw_json, updated_at, search_text,
		                      keywords_json, has_photo)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, id) DO UPDATE SET
			address_book_ids = excluded.address_book_ids, uid = excluded.uid, kind = excluded.kind,
			display_name = excluded.display_name, sort_name = excluded.sort_name,
			emails_json = excluded.emails_json, phones_json = excluded.phones_json,
			organization = excluded.organization, raw_json = excluded.raw_json,
			updated_at = excluded.updated_at, search_text = excluded.search_text,
			keywords_json = excluded.keywords_json, has_photo = excluded.has_photo`)
	if err != nil {
		return wrap(CodeQuery, "prepare contact upsert", err)
	}
	defer stmt.Close()

	for _, c := range list {
		books, _ := json.Marshal(c.AddressBookIDs)
		emails, _ := json.Marshal(c.Emails)
		phones, _ := json.Marshal(c.Phones)

		search := []string{c.DisplayName, c.Organization}
		for _, e := range c.Emails {
			search = append(search, e.Email)
		}
		search = append(search, c.Phones...)
		search = append(search, c.Keywords...)
		keywords, _ := json.Marshal(c.Keywords)
		if c.Keywords == nil {
			keywords = []byte("[]")
		}

		if _, err := stmt.ExecContext(ctx, accountID, c.ID, string(books), c.UID, c.Kind, c.DisplayName, c.SortName,
			string(emails), string(phones), c.Organization, c.Raw, c.UpdatedAt,
			strings.ToLower(strings.Join(search, " ")), string(keywords), boolToInt(c.HasPhoto),
		); err != nil {
			return wrap(CodeQuery, "upsert contact", err)
		}
	}
	return nil
}

// Contacts 列出联系人。accountID / bookID 为空表示不按其过滤; query 为空表示不检索。
func (s *Store) Contacts(ctx context.Context, accountID, bookID, query string, limit int) ([]ContactSummary, error) {
	sqlText := `
		SELECT account_id, id, address_book_ids, display_name, organization, emails_json, phones_json,
		       kind, keywords_json, has_photo
		FROM contacts c WHERE 1 = 1`
	var args []any
	if accountID != "" {
		sqlText += ` AND account_id = ?`
		args = append(args, accountID)
	}
	if bookID != "" {
		sqlText += ` AND EXISTS (SELECT 1 FROM json_each(c.address_book_ids) WHERE value = ?)`
		args = append(args, bookID)
	}
	if q := strings.TrimSpace(query); q != "" {
		sqlText += ` AND search_text LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(strings.ToLower(q))+"%")
	}
	sqlText += ` ORDER BY sort_name LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "list contacts", err)
	}
	defer rows.Close()

	var out []ContactSummary
	for rows.Next() {
		var c ContactSummary
		var books, emails, phones, keywords string
		var hasPhoto int
		if err := rows.Scan(&c.AccountID, &c.ID, &books, &c.DisplayName, &c.Organization, &emails, &phones,
			&c.Kind, &keywords, &hasPhoto); err != nil {
			return nil, wrap(CodeQuery, "scan contact", err)
		}
		_ = json.Unmarshal([]byte(books), &c.AddressBookIDs)
		_ = json.Unmarshal([]byte(keywords), &c.Keywords)
		c.HasPhoto = hasPhoto != 0
		var e []ContactEmailRow
		if json.Unmarshal([]byte(emails), &e) == nil && len(e) > 0 {
			c.Email = e[0].Email
		}
		var p []string
		if json.Unmarshal([]byte(phones), &p) == nil && len(p) > 0 {
			c.Phone = p[0]
		}
		out = append(out, c)
	}
	return out, wrap(CodeQuery, "iterate contacts", rows.Err())
}

// ContactUIDs 返回 id → uid(只含存在的联系人)。
func (s *Store) ContactUIDs(ctx context.Context, accountID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, uid FROM contacts WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, wrap(CodeQuery, "contact uids", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, uid string
		if err := rows.Scan(&id, &uid); err != nil {
			return nil, wrap(CodeQuery, "scan contact uid", err)
		}
		out[id] = uid
	}
	return out, wrap(CodeQuery, "iterate contact uids", rows.Err())
}

// Contact 读单个联系人原文。
func (s *Store) Contact(ctx context.Context, accountID, id string) (string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT raw_json FROM contacts WHERE account_id = ? AND id = ?`, accountID, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return raw, wrap(CodeQuery, "read contact", err)
}

// SearchRecipients 在全部账号的联系人里按姓名或地址前缀/子串检索邮箱, 供收件人补全。
func (s *Store) SearchRecipients(ctx context.Context, query string, limit int) ([]Recipient, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	like := "%" + escapeLike(q) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT c.display_name, json_extract(e.value, '$.email') AS email
		FROM contacts c, json_each(c.emails_json) e
		WHERE LOWER(c.display_name) LIKE ? ESCAPE '\' OR LOWER(json_extract(e.value, '$.email')) LIKE ? ESCAPE '\'
		ORDER BY
			CASE WHEN LOWER(json_extract(e.value, '$.email')) LIKE ? ESCAPE '\' THEN 0
			     WHEN LOWER(c.display_name) LIKE ? ESCAPE '\' THEN 1 ELSE 2 END,
			c.sort_name
		LIMIT ?`, like, like, escapeLike(q)+"%", escapeLike(q)+"%", limit)
	if err != nil {
		return nil, wrap(CodeQuery, "search recipients", err)
	}
	defer rows.Close()

	var out []Recipient
	for rows.Next() {
		var r Recipient
		var email sql.NullString
		if err := rows.Scan(&r.Name, &email); err != nil {
			return nil, wrap(CodeQuery, "scan recipient", err)
		}
		if !email.Valid || email.String == "" {
			continue
		}
		r.Email = email.String
		out = append(out, r)
	}
	return out, wrap(CodeQuery, "iterate recipients", rows.Err())
}
