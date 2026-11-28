//go:build !android

package sqlitedb

import (
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 驱动
)

const driverName = "sqlite"

func dsn(path string, p Pragmas) string {
	var q []string
	if p.JournalMode != "" {
		q = append(q, "_pragma=journal_mode("+p.JournalMode+")")
	}
	if p.BusyTimeoutMs > 0 {
		q = append(q, fmt.Sprintf("_pragma=busy_timeout(%d)", p.BusyTimeoutMs))
	}
	if p.Synchronous != "" {
		q = append(q, "_pragma=synchronous("+p.Synchronous+")")
	}
	if p.ForeignKeys {
		q = append(q, "_pragma=foreign_keys(ON)")
	}
	if p.QueryOnly {
		q = append(q, "_pragma=query_only(1)")
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + strings.Join(q, "&")
}
