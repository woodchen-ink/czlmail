package model

import "time"

// EmailSubmission is one sent message (RFC 8621 §7), kept for seven days. The
// gateway sends at once, so undoStatus is always final.
type EmailSubmission struct {
	ID         uint   `gorm:"primaryKey"`
	PublicID   string `gorm:"size:32;uniqueIndex;not null"` // JMAP id
	AccountID  string `gorm:"size:32;not null;index:idx_submissions_dedupe,priority:1;index:idx_submissions_account_sent,priority:1"`
	IdentityID string `gorm:"size:32;not null"`
	EmailID    string `gorm:"size:32;not null;index:idx_submissions_dedupe,priority:2"`
	ThreadID   string `gorm:"size:32;not null"`
	// Envelope is the JSON JMAP Envelope used.
	Envelope string    `gorm:"type:text;not null"`
	SendAt   time.Time `gorm:"not null;index:idx_submissions_account_sent,priority:2"`
	// UndoStatus is "final".
	UndoStatus string `gorm:"size:16;not null"`
	// DeliveryStatus is the JSON map of recipient → JMAP DeliveryStatus.
	DeliveryStatus string `gorm:"type:text;not null"`
	// ContentHash is the SHA-256 of the message as fetched, for the duplicate guard.
	ContentHash string `gorm:"size:64;not null"`
	// MessageID is the delivered message's Message-ID (also for the guard).
	MessageID string    `gorm:"size:998"`
	CreatedAt time.Time `gorm:"not null;index"`
}

func (EmailSubmission) TableName() string { return "email_submissions" }

// Identity sources.
const (
	IdentityPrimary  = "primary"  // the upstream's own address; follows Upstream.Email
	IdentityAlias    = "alias"    // registered by the user (admin or Identity/set)
	IdentityProvider = "provider" // listed by the upstream (upstream.IdentitySource: Gmail send-as)
)

// Identity is a From address of an account (RFC 8621 §6). The address may be
// "*@domain": any local part of that domain.
type Identity struct {
	ID        uint   `gorm:"primaryKey"`
	PublicID  string `gorm:"size:32;uniqueIndex;not null"` // JMAP id
	AccountID string `gorm:"size:32;not null;index"`
	Source    string `gorm:"size:16;not null"`
	// ProviderKey is the upstream's id of a provider identity (also its PublicID).
	ProviderKey   string `gorm:"size:300"`
	Email         string `gorm:"size:254;not null"`
	Name          string `gorm:"size:256"`
	ReplyTo       string `gorm:"type:text"` // JSON [{name,email}] or empty
	Bcc           string `gorm:"type:text"` // JSON [{name,email}] or empty
	TextSignature string `gorm:"type:text"`
	HTMLSignature string `gorm:"type:text"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (Identity) TableName() string { return "identities" }

// ObjectChange is a changelog entry of a gateway-stored JMAP type (EmailSubmission,
// Identity). States are the account's latest Seq of the type.
type ObjectChange struct {
	Seq       int64     `gorm:"primaryKey;autoIncrement"`
	AccountID string    `gorm:"size:32;not null;index:idx_object_changes,priority:1"`
	Type      string    `gorm:"size:32;not null;index:idx_object_changes,priority:2"`
	ObjectID  string    `gorm:"size:32;not null"`
	Kind      string    `gorm:"size:16;not null"` // created, updated, destroyed
	CreatedAt time.Time `gorm:"not null;index"`
}

func (ObjectChange) TableName() string { return "object_changes" }

// ObjectChangeFloor is the oldest state of a type /changes can still answer from
// after old entries were pruned.
type ObjectChangeFloor struct {
	AccountID string `gorm:"primaryKey;size:32"`
	Type      string `gorm:"primaryKey;size:32"`
	MinSeq    int64  `gorm:"not null"`
}

func (ObjectChangeFloor) TableName() string { return "object_change_floors" }
