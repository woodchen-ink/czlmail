package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Calendar 是日历容器。
type Calendar struct {
	AccountID    string          `json:"accountId"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Color        string          `json:"color"`
	SortOrder    int64           `json:"sortOrder"`
	IsVisible    bool            `json:"isVisible"`
	IsSubscribed bool            `json:"isSubscribed"`
	IsDefault    bool            `json:"isDefault"`
	MyRights     map[string]bool `json:"myRights"`
	// DefaultAlertsWithTime / WithoutTime 是原始 JSON, 供提醒计算。
	DefaultAlertsWithTime    string `json:"-"`
	DefaultAlertsWithoutTime string `json:"-"`
}

// EventRow 是日历事件落库的形态。展示字段从 Raw 还原, 其余列只服务于查询。
type EventRow struct {
	ID          string
	CalendarIDs []string
	UID         string
	Title       string
	Description string
	Location    string
	StartAt     int64
	// EndAt 为 nil 表示无限重复。
	EndAt     *int64
	AllDay    bool
	TimeZone  string
	Recurring bool
	Status    string
	Raw       string
	UpdatedAt int64
}

// RawObject 是账号下的一条原始 JSON 对象。
type RawObject struct {
	AccountID string
	ID        string
	Raw       string
}

// ReplaceCalendars 用全量列表替换某账号的日历。日历数量很少, 每次都全量取。
func ReplaceCalendars(ctx context.Context, tx *sql.Tx, accountID string, list []Calendar) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM calendars WHERE account_id = ?`, accountID); err != nil {
		return wrap(CodeQuery, "clear calendars", err)
	}
	for _, c := range list {
		rights, _ := json.Marshal(c.MyRights)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO calendars (account_id, id, name, description, color, sort_order,
			                       is_visible, is_subscribed, is_default, my_rights,
			                       default_alerts_with_time, default_alerts_without_time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, c.ID, c.Name, c.Description, c.Color, c.SortOrder,
			boolToInt(c.IsVisible), boolToInt(c.IsSubscribed), boolToInt(c.IsDefault), string(rights),
			c.DefaultAlertsWithTime, c.DefaultAlertsWithoutTime,
		); err != nil {
			return wrap(CodeQuery, "insert calendar", err)
		}
	}
	return nil
}

// Calendars 列出全部账号的日历, 个人账号在前。
func (s *Store) Calendars(ctx context.Context) ([]Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.account_id, c.id, c.name, c.description, c.color, c.sort_order,
		       c.is_visible, c.is_subscribed, c.is_default, c.my_rights,
		       c.default_alerts_with_time, c.default_alerts_without_time
		FROM calendars c
		LEFT JOIN accounts a ON a.id = c.account_id
		ORDER BY COALESCE(a.is_personal, 0) DESC, c.account_id, c.is_default DESC, c.sort_order, c.name`)
	if err != nil {
		return nil, wrap(CodeQuery, "list calendars", err)
	}
	defer rows.Close()

	var out []Calendar
	for rows.Next() {
		var c Calendar
		var visible, subscribed, isDefault int
		var rights string
		if err := rows.Scan(&c.AccountID, &c.ID, &c.Name, &c.Description, &c.Color, &c.SortOrder,
			&visible, &subscribed, &isDefault, &rights,
			&c.DefaultAlertsWithTime, &c.DefaultAlertsWithoutTime); err != nil {
			return nil, wrap(CodeQuery, "scan calendar", err)
		}
		c.IsVisible, c.IsSubscribed, c.IsDefault = visible != 0, subscribed != 0, isDefault != 0
		_ = json.Unmarshal([]byte(rights), &c.MyRights)
		out = append(out, c)
	}
	return out, wrap(CodeQuery, "iterate calendars", rows.Err())
}

// UpsertEvents 写入事件。
func UpsertEvents(ctx context.Context, tx *sql.Tx, accountID string, events []EventRow) error {
	if len(events) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO calendar_events (account_id, id, calendar_ids, uid, title, description, location,
		                             start_at, end_at, is_all_day, time_zone, is_recurring, status,
		                             raw_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, id) DO UPDATE SET
			calendar_ids = excluded.calendar_ids, uid = excluded.uid, title = excluded.title,
			description = excluded.description, location = excluded.location,
			start_at = excluded.start_at, end_at = excluded.end_at, is_all_day = excluded.is_all_day,
			time_zone = excluded.time_zone, is_recurring = excluded.is_recurring, status = excluded.status,
			raw_json = excluded.raw_json, updated_at = excluded.updated_at`)
	if err != nil {
		return wrap(CodeQuery, "prepare event upsert", err)
	}
	defer stmt.Close()

	for _, e := range events {
		ids, _ := json.Marshal(e.CalendarIDs)
		var end any
		if e.EndAt != nil {
			end = *e.EndAt
		}
		if _, err := stmt.ExecContext(ctx, accountID, e.ID, string(ids), e.UID, e.Title, e.Description, e.Location,
			e.StartAt, end, boolToInt(e.AllDay), e.TimeZone, boolToInt(e.Recurring), e.Status,
			e.Raw, e.UpdatedAt); err != nil {
			return wrap(CodeQuery, "upsert event", err)
		}
	}
	return nil
}

// EventsInRange 取可能与 [from, to) 有交集的事件原文, 由调用方展开重复并精确过滤。
// accountIDs 为空时取全部账号。
func (s *Store) EventsInRange(ctx context.Context, accountIDs []string, from, to int64) ([]RawObject, error) {
	query := `
		SELECT account_id, id, raw_json FROM calendar_events
		WHERE start_at < ? AND (end_at IS NULL OR end_at >= ? OR is_recurring = 1)`
	args := []any{to, from}
	if len(accountIDs) > 0 {
		query += ` AND account_id IN (?` + strings.Repeat(",?", len(accountIDs)-1) + `)`
		for _, id := range accountIDs {
			args = append(args, id)
		}
	}
	return s.rawObjects(ctx, query, args...)
}

// AllEventObjects 返回全部事件与任务的原文。
func (s *Store) AllEventObjects(ctx context.Context) ([]RawObject, error) {
	return s.rawObjects(ctx, `SELECT account_id, id, raw_json FROM calendar_events`)
}

// SearchEvents 按标题、地点、描述检索事件原文。
func (s *Store) SearchEvents(ctx context.Context, query string, limit int) ([]RawObject, error) {
	like := "%" + escapeLike(strings.ToLower(query)) + "%"
	return s.rawObjects(ctx, `
		SELECT account_id, id, raw_json FROM calendar_events
		WHERE LOWER(title) LIKE ? ESCAPE '\' OR LOWER(location) LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\'
		ORDER BY start_at DESC LIMIT ?`, like, like, like, limit)
}

// Event 读单个事件原文。
func (s *Store) Event(ctx context.Context, accountID, id string) (string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT raw_json FROM calendar_events WHERE account_id = ? AND id = ?`, accountID, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return raw, wrap(CodeQuery, "read event", err)
}

// EventIDByUID 按 iCalendar UID 找本地事件, 找不到返回空串。
func (s *Store) EventIDByUID(ctx context.Context, accountID, uid string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM calendar_events WHERE account_id = ? AND uid = ? LIMIT 1`, accountID, uid).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, wrap(CodeQuery, "find event by uid", err)
}

func (s *Store) rawObjects(ctx context.Context, query string, args ...any) ([]RawObject, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query objects", err)
	}
	defer rows.Close()
	var out []RawObject
	for rows.Next() {
		var o RawObject
		if err := rows.Scan(&o.AccountID, &o.ID, &o.Raw); err != nil {
			return nil, wrap(CodeQuery, "scan object", err)
		}
		out = append(out, o)
	}
	return out, wrap(CodeQuery, "iterate objects", rows.Err())
}

// DeleteObjects 按 id 删除某张 PIM 表里的行。table 只接受内部常量。
func DeleteObjects(ctx context.Context, tx *sql.Tx, table, accountID string, ids []string) error {
	if !pimTables[table] {
		return &Error{Code: CodeQuery, Msg: "unknown table " + table}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id = ? AND id = ?`, accountID, id); err != nil {
			return wrap(CodeQuery, "delete from "+table, err)
		}
	}
	return nil
}

// ClearObjects 清空某账号在某张 PIM 表里的全部行, 用于重新引导。
func ClearObjects(ctx context.Context, tx *sql.Tx, table, accountID string) error {
	if !pimTables[table] {
		return &Error{Code: CodeQuery, Msg: "unknown table " + table}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id = ?`, accountID)
	return wrap(CodeQuery, "clear "+table, err)
}

// PIM 表名。DeleteObjects / ClearObjects 拼接表名, 只允许这些值。
const (
	TableCalendars    = "calendars"
	TableEvents       = "calendar_events"
	TableAddressBooks = "address_books"
	TableContacts     = "contacts"
	TableFileNodes    = "file_nodes"
)

var pimTables = map[string]bool{
	TableCalendars: true, TableEvents: true, TableAddressBooks: true, TableContacts: true, TableFileNodes: true,
}
