package model

import "time"

// AppPassword is a per-device password for JMAP Basic authentication. The full value
// (czl_<prefix>_<secret>) is shown once at creation; the row keeps the prefix for lookup
// and an argon2id hash of the whole value. Revoking deletes the row.
type AppPassword struct {
	ID         uint   `gorm:"primaryKey"`
	PublicID   string `gorm:"size:36;uniqueIndex;not null"`
	UserID     uint   `gorm:"not null;index"`
	Name       string `gorm:"size:64;not null"`
	Prefix     string `gorm:"size:16;uniqueIndex;not null"`
	Hash       string `gorm:"not null"`
	LastUsedAt *time.Time
	LastUsedIP string `gorm:"size:64"`
	CreatedAt  time.Time
}
