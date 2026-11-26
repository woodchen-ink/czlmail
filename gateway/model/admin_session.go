package model

import "time"

// AdminSession is a signed-in admin browser. The cookie carries a random token; only its
// SHA-256 is stored, so a copy of the database cannot be used to sign in.
type AdminSession struct {
	ID         uint   `gorm:"primaryKey"`
	TokenHash  string `gorm:"size:64;uniqueIndex;not null"` // hex SHA-256 of the cookie value
	UserID     uint   `gorm:"not null;index"`
	IP         string `gorm:"size:64"`
	UserAgent  string `gorm:"size:256"`
	CreatedAt  time.Time
	LastSeenAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"` // absolute limit; idle sessions end earlier
}
