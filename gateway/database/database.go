// Package database opens the gateway's SQLite files.
//
// data/gateway.sqlite holds users, upstream settings, secrets and audit records.
// Each upstream gets its own file under data/upstreams/ for its index and changelog,
// so upstreams never wait on each other's writes and removing one deletes one file.
package database

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// MainFile is the name of the main database inside the data directory.
const MainFile = "gateway.sqlite"

// Open opens (and creates) the main database and migrates its schema.
func Open(dataDir string) (*gorm.DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := openFile(filepath.Join(dataDir, MainFile))
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&model.Meta{}, &model.Setting{}, &model.User{}, &model.AdminSession{}, &model.AppPassword{}, &model.AuditLog{}, &model.PushSubscription{}, &model.Upstream{}, &model.UpstreamMetricHour{},
		&model.EmailSubmission{}, &model.Identity{}, &model.ObjectChange{}, &model.ObjectChangeFloor{}, &model.OAuthApp{}); err != nil {
		return nil, fmt.Errorf("migrate %s: %w", MainFile, err)
	}
	return db, nil
}

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// UpstreamPath is the file holding one upstream's index and changelog.
func UpstreamPath(dataDir, accountID string) (string, error) {
	if !accountIDPattern.MatchString(accountID) {
		return "", fmt.Errorf("invalid account id %q", accountID)
	}
	return filepath.Join(dataDir, "upstreams", accountID+".sqlite"), nil
}

// OpenUpstream opens (and creates) one upstream's database. The caller migrates it.
func OpenUpstream(dataDir, accountID string) (*gorm.DB, error) {
	path, err := UpstreamPath(dataDir, accountID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create upstream dir: %w", err)
	}
	return openFile(path)
}

func openFile(path string) (*gorm.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	// BEGIN IMMEDIATE: a transaction that reads and then writes takes the write
	// lock up front and waits (busy_timeout) for other writers. A deferred one
	// fails at once with SQLITE_BUSY when another connection committed after its
	// read, which busy_timeout cannot help (WAL snapshot upgrade).
	q.Add("_txlock", "immediate")
	db, err := gorm.Open(sqlite.Open(path+"?"+q.Encode()), &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	return db, nil
}

// Close closes the underlying connection pool.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
