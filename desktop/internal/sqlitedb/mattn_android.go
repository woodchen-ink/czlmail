package sqlitedb

// FTS5 要用 sqlite_fts5 构建标签开启 (android/scripts/build-core.sh 已传); 漏传时缓存库建 FTS 表会报
// "no such module: fts5", 启动即失败, 不会悄悄退化。

import (
	"fmt"
	"net/url"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const driverName = "sqlite3"

func dsn(path string, p Pragmas) string {
	var q []string
	if p.JournalMode != "" {
		q = append(q, "_journal_mode="+p.JournalMode)
	}
	if p.BusyTimeoutMs > 0 {
		q = append(q, fmt.Sprintf("_busy_timeout=%d", p.BusyTimeoutMs))
	}
	if p.Synchronous != "" {
		q = append(q, "_synchronous="+p.Synchronous)
	}
	if p.ForeignKeys {
		q = append(q, "_foreign_keys=on")
	}
	u := "file:" + (&url.URL{Path: path}).EscapedPath()
	if len(q) == 0 {
		return u
	}
	return u + "?" + strings.Join(q, "&")
}
