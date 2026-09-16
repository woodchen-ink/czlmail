package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DataType 是 JMAP 的对象类型名, 同时用作 sync_state 的分区键。
// 取值必须与服务端 StateChange 推送里的 key 完全一致, 否则增量同步对不上。
type DataType string

const (
	TypeMailbox       DataType = "Mailbox"
	TypeEmail         DataType = "Email"
	TypeThread        DataType = "Thread"
	TypeCalendar      DataType = "Calendar"
	TypeCalendarEvent DataType = "CalendarEvent"
	TypeAddressBook   DataType = "AddressBook"
	TypeContactCard   DataType = "ContactCard"
	TypeFileNode      DataType = "FileNode"
)

// State 返回本地已同步到的 state token。首次同步时不存在记录, 返回空串且 err 为 nil ——
// 调用方据此区分"需要全量引导"与"可以走 /changes 增量"。
func (s *Store) State(ctx context.Context, accountID string, dt DataType) (string, error) {
	var state string
	err := s.db.QueryRowContext(ctx,
		`SELECT state FROM sync_state WHERE account_id = ? AND data_type = ?`,
		accountID, dt,
	).Scan(&state)

	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", wrap(CodeQuery, "read sync state", err)
	}
	return state, nil
}

// SetState 记录新的 state token。
//
// 必须与写入数据的操作处于同一事务, 否则一旦在"数据已写、token 未写"之间崩溃,
// 下次启动会重复拉同一批变更; 反过来在"token 已写、数据未写"之间崩溃则会永久丢失
// 那批变更, 且无法察觉。故本方法只接受 tx, 不提供脱离事务的版本。
func SetState(ctx context.Context, tx *sql.Tx, accountID string, dt DataType, state string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO sync_state (account_id, data_type, state, synced_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (account_id, data_type)
		 DO UPDATE SET state = excluded.state, synced_at = excluded.synced_at`,
		accountID, dt, state, time.Now().Unix(),
	)
	return wrap(CodeQuery, "write sync state", err)
}

// ResetState 删除某账号某类型的 state token, 使下次同步退回全量引导。
// 服务端返回 cannotCalculateChanges 时由 syncer 调用。
func ResetState(ctx context.Context, tx *sql.Tx, accountID string, dt DataType) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM sync_state WHERE account_id = ? AND data_type = ?`,
		accountID, dt,
	)
	return wrap(CodeQuery, "reset sync state", err)
}
