package model

import "time"

// Roles. The owner is the first user, created by the setup link; there is exactly one.
const (
	RoleOwner = "owner"
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is a person who signs in to the gateway: to the admin with email + password,
// and from czlmail with an app password. ID never leaves the server; PublicID does.
type User struct {
	ID           uint   `gorm:"primaryKey"`
	PublicID     string `gorm:"size:36;uniqueIndex;not null"`
	Email        string `gorm:"size:254;uniqueIndex;not null"` // lower-case
	Name         string `gorm:"size:64;not null"`
	PasswordHash string `gorm:"not null"` // argon2id, admin sign-in only; never accepted for Basic
	Role         string `gorm:"size:16;not null;index"`
	Disabled     bool   `gorm:"not null;default:false"`
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
