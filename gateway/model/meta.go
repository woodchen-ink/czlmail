// Package model holds the GORM structs of the gateway database.
package model

import "time"

// Meta is internal bookkeeping that is never edited in the admin,
// such as the master key version and its check value.
type Meta struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"not null"`
	UpdatedAt time.Time
}

// Meta keys.
const (
	MetaMasterKeyVersion = "master_key_version"
	MetaMasterKeyCheck   = "master_key_check"
)

// Setting is one admin-editable configuration value, stored as JSON.
// Keys are grouped by prefix (site., security., oauth., proxy., sync., limits.) and registered
// with their type and default in one place; business code never reads this table directly.
type Setting struct {
	Key       string `gorm:"primaryKey;size:128"`
	Value     string `gorm:"type:text;not null"` // JSON
	UpdatedAt time.Time
	UpdatedBy string `gorm:"size:64"` // public id of the admin who changed it
}
