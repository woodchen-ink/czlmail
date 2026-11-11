// Package sqlitedb 按平台选 SQLite 驱动打开数据库, 调用方只依赖本包, 不直接 import 驱动。
//
// 桌面端与 iOS 用纯 Go 的 modernc.org/sqlite (不引入 CGO)。Android 改用 cgo 的 mattn/go-sqlite3:
// modernc 底层是转译的 musl, 在 x86_64 上调用 lstat/open/stat 等旧式系统调用, Android 的 seccomp 只放行 *at 系列,
// 进程直接被 SIGSYS 杀掉; Android 内核本来就以 cgo 构建, 换成 cgo 驱动不增加依赖。
// 两个驱动都要有 FTS5 (缓存库的全文检索用 trigram 分词): modernc 自带, mattn 靠 sqlite_fts5 构建标签。
package sqlitedb

import "database/sql"

// Pragmas 是每条连接打开时设置的参数。必须写进连接串而不是打开后 Exec:
// 否则只有连接池里第一条连接被设置到。
type Pragmas struct {
	JournalMode   string // 如 "WAL"
	Synchronous   string // 如 "NORMAL" / "FULL"
	BusyTimeoutMs int
	ForeignKeys   bool
}

// Open 打开 path 处的数据库 (不存在时创建)。
func Open(path string, p Pragmas) (*sql.DB, error) {
	return sql.Open(driverName, dsn(path, p))
}
