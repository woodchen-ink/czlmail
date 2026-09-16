// Package store 是 czlmail 的本地缓存层: 邮件、日历、通讯录、文件的元数据全部
// 落 SQLite, 由 syncer 按 JMAP 的 state token 增量维护。桌面端与 MCP server 共用本包。
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // 纯 Go 驱动, 不引入 CGO, 见仓库 CLAUDE.md 的依赖约束
)

// Store 持有单个 SQLite 连接池。SQLite 的写入是全库串行的, 因此写连接限制为 1,
// 由驱动排队而不是让并发写撞上 SQLITE_BUSY。
type Store struct {
	db *sql.DB
}

// pragmas 在每条连接建立时生效, 必须走 DSN 而不是 Open 之后执行一次,
// 否则只有连接池里第一条连接被设置到。
//
//   - journal_mode=WAL: 读写不互相阻塞, 同步线程写入时界面仍可查询
//   - busy_timeout: 撞锁时driver内部重试而非立刻报错
//   - synchronous=NORMAL: WAL 下的推荐档位, 崩溃最多丢最后一个事务,
//     缓存数据可从服务端重新拉取, 不需要 FULL 的代价
//   - foreign_keys=ON: SQLite 默认关闭外键约束
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=foreign_keys(ON)"

// Open 打开或创建数据库并把 schema 升到当前版本。
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, wrap(CodeOpen, "open database", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, wrap(CodeOpen, "ping database", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// DB 暴露底层句柄给同包内的查询实现, 不对外导出使用。
func (s *Store) DB() *sql.DB { return s.db }

// migrate 用 SQLite 的 user_version 记录已应用的迁移数, 逐条补齐到 schemaVersion。
// 每条迁移连同版本号的推进在同一个事务里提交, 中断不会留下半截 schema。
func (s *Store) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return wrap(CodeMigrate, "read user_version", err)
	}

	if current > schemaVersion {
		return &Error{
			Code: CodeMigrate,
			Msg: fmt.Sprintf("database schema version %d is newer than supported version %d",
				current, schemaVersion),
		}
	}

	for v := current; v < schemaVersion; v++ {
		if err := s.applyMigration(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, from int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap(CodeMigrate, "begin migration", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migrations[from]); err != nil {
		return wrap(CodeMigrate, fmt.Sprintf("apply migration %d", from+1), err)
	}

	// user_version 不接受参数绑定, 只能拼接; from 来自内部循环, 非外部输入。
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", from+1)); err != nil {
		return wrap(CodeMigrate, "bump user_version", err)
	}

	return wrap(CodeMigrate, "commit migration", tx.Commit())
}

// WithTx 把 fn 包在一个事务里, fn 返回错误或 panic 时回滚。
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap(CodeTransaction, "begin transaction", err)
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	return wrap(CodeTransaction, "commit transaction", tx.Commit())
}
