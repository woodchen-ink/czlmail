package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/model"
)

// MetaMasterKeyBackupConfirmed records when an admin confirmed the master key is backed up.
const MetaMasterKeyBackupConfirmed = "master_key_backup_confirmed_at"

// SystemInfo is the admin's system status.
type SystemInfo struct {
	Version   string    `json:"version"`
	GoVersion string    `json:"goVersion"`
	StartedAt time.Time `json:"startedAt"`
	BaseURL   string    `json:"baseUrl"`
	TimeZone  string    `json:"timeZone"`
	Database  struct {
		OK            bool   `json:"ok"`
		MainBytes     int64  `json:"mainBytes"`
		UpstreamBytes int64  `json:"upstreamBytes"`
		Error         string `json:"error,omitempty"`
	} `json:"database"`
	MasterKey struct {
		Version           uint32     `json:"version"`
		BackupConfirmedAt *time.Time `json:"backupConfirmedAt"`
	} `json:"masterKey"`
	OnlineClients int             `json:"onlineClients"`
	AdminWeb      bool            `json:"adminWeb"` // the admin pages are installed
	Env           []config.EnvVar `json:"env"`
}

// System reports the gateway's own status for admins.
type System struct {
	db        *gorm.DB
	dataDir   string
	baseURL   string
	version   string
	startedAt time.Time
	env       []config.EnvVar
	streams   func(userID string) int
	adminWeb  bool
	now       func() time.Time
}

// SystemOptions configure NewSystem.
type SystemOptions struct {
	DataDir, BaseURL, Version string
	Env                       []config.EnvVar
	// Streams counts open EventSource connections; "" counts all users.
	Streams  func(userID string) int
	AdminWeb bool
}

// NewSystem returns the system status service; the start time is now.
func NewSystem(db *gorm.DB, o SystemOptions) *System {
	if o.Streams == nil {
		o.Streams = func(string) int { return 0 }
	}
	return &System{
		db: db, dataDir: o.DataDir, baseURL: o.BaseURL, version: o.Version, env: o.Env,
		streams: o.Streams, adminWeb: o.AdminWeb, startedAt: time.Now().UTC(), now: time.Now,
	}
}

// BaseURL is the gateway's public origin.
func (s *System) BaseURL() string { return s.baseURL }

// Info returns the system status. Admins only.
func (s *System) Info(actor *model.User) (*SystemInfo, error) {
	if !IsAdmin(actor) {
		return nil, ErrForbidden
	}
	info := &SystemInfo{
		Version: s.version, GoVersion: runtime.Version(), StartedAt: s.startedAt, BaseURL: s.baseURL,
		TimeZone: zoneName(s.now()), OnlineClients: s.streams(""), AdminWeb: s.adminWeb, Env: s.env,
	}
	if sqlDB, err := s.db.DB(); err != nil {
		info.Database.Error = "unavailable"
	} else if err := sqlDB.Ping(); err != nil {
		info.Database.Error = "unavailable"
	} else {
		info.Database.OK = true
	}
	info.Database.MainBytes = fileSizes(filepath.Join(s.dataDir, "gateway.sqlite*"))
	info.Database.UpstreamBytes = dirSize(filepath.Join(s.dataDir, "upstreams"))
	version, _, err := readKeyMeta(s.db)
	if err != nil {
		return nil, err
	}
	info.MasterKey.Version = version
	if info.MasterKey.BackupConfirmedAt, err = s.MasterKeyBackupConfirmedAt(); err != nil {
		return nil, err
	}
	return info, nil
}

// MasterKeyBackupConfirmedAt is when an admin confirmed the master key backup, or nil.
func (s *System) MasterKeyBackupConfirmedAt() (*time.Time, error) {
	var m model.Meta
	err := s.db.Where("`key` = ?", MetaMasterKeyBackupConfirmed).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, m.Value)
	if err != nil {
		return nil, nil // unreadable: ask again
	}
	return &t, nil
}

// ConfirmMasterKeyBackup records that the master key is backed up. Admins only.
func (s *System) ConfirmMasterKeyBackup(actor *model.User) (time.Time, error) {
	if !IsAdmin(actor) {
		return time.Time{}, ErrForbidden
	}
	now := s.now().UTC().Truncate(time.Second)
	row := model.Meta{Key: MetaMasterKeyBackupConfirmed, Value: now.Format(time.RFC3339)}
	return now, s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
}

// zoneName is the process time zone (TZ) as "Asia/Shanghai (UTC+08:00)"; Windows reports "Local".
func zoneName(now time.Time) string {
	_, off := now.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	utc := fmt.Sprintf("UTC%s%02d:%02d", sign, off/3600, off%3600/60)
	if name := now.Location().String(); name != "Local" {
		return name + " (" + utc + ")"
	}
	return utc
}

func fileSizes(pattern string) int64 {
	matches, _ := filepath.Glob(pattern)
	var n int64
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
			n += st.Size()
		}
	}
	return n
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if st, err := d.Info(); err == nil {
				n += st.Size()
			}
		}
		return nil
	})
	return n
}
