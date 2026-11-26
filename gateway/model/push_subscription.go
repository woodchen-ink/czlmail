package model

import "time"

// PushSubscription is a JMAP PushSubscription (RFC 8620 §7.2) of a gateway user.
// URL and Auth are sealed with the master key (purpose push-key): together with
// the public P256DH they let anyone send notifications to the device.
type PushSubscription struct {
	ID               uint      `gorm:"primaryKey"`
	PublicID         string    `gorm:"size:40;uniqueIndex;not null"` // the JMAP id
	UserPublicID     string    `gorm:"size:36;index;not null"`
	CredentialID     string    `gorm:"size:36;index;not null;default:''"` // app password public id
	DeviceClientID   string    `gorm:"size:255;not null"`
	URL              string    `gorm:"column:url;type:text;not null"` // sealed
	P256DH           string    `gorm:"column:p256dh;size:100"`        // base64url; empty without keys
	Auth             string    `gorm:"column:auth;type:text"`         // sealed; empty without keys
	VerificationCode string    `gorm:"size:64;not null"`
	Verified         bool      `gorm:"not null;default:false"`
	Expires          time.Time `gorm:"index;not null"`
	Types            string    `gorm:"type:text;not null"` // JSON: null = all types
	EmailPush        string    `gorm:"type:text;not null"` // JSON: accountId → EmailPushConfig, or null
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
