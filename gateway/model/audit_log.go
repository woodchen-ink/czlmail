package model

import "time"

// AuditLog records one admin write request. Params is a JSON summary of the request body
// with secret fields masked; response bodies are never stored.
type AuditLog struct {
	ID         uint      `gorm:"primaryKey"`
	PublicID   string    `gorm:"size:36;uniqueIndex;not null"` // time-ordered, used as the page cursor
	ActorID    string    `gorm:"size:36;index"`                // user public id; empty before sign-in
	ActorEmail string    `gorm:"size:254"`
	Method     string    `gorm:"size:8;not null"`
	Path       string    `gorm:"size:256;not null"`
	Params     string    `gorm:"type:text"`
	Status     int       `gorm:"not null"` // HTTP status
	Code       int       `gorm:"not null"` // response code
	Msg        string    `gorm:"size:64"`
	IP         string    `gorm:"size:64"`
	TraceID    string    `gorm:"size:32"`
	CreatedAt  time.Time `gorm:"index"`
}
