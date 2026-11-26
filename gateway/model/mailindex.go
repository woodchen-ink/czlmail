package model

import "time"

// The tables below live in one upstream's own database (data/upstreams/<accountId>.sqlite),
// never in gateway.sqlite. service/mailindex owns them.

// IndexMeta is the single row (ID 1) describing the index and its changelog.
type IndexMeta struct {
	ID uint `gorm:"primaryKey"`
	// Generation is the first half of every state string. It is the index's creation
	// time in Unix seconds, so a recreated index never accepts an old state.
	Generation int64 `gorm:"not null"`
	// Seq is the last changelog sequence number handed out.
	Seq int64 `gorm:"not null;default:0"`
	// MailboxSeq, EmailSeq and ThreadSeq are the last seq of each type: the type's state.
	MailboxSeq int64 `gorm:"not null;default:0"`
	EmailSeq   int64 `gorm:"not null;default:0"`
	ThreadSeq  int64 `gorm:"not null;default:0"`
	// MinSeq: changes since a state below it cannot be calculated (trimmed).
	MinSeq int64 `gorm:"not null;default:0"`
	// Ready is set once the first full import finished; before that there is no state.
	Ready        bool `gorm:"not null;default:false"`
	LastFullSync *time.Time
	UpdatedAt    time.Time
}

func (IndexMeta) TableName() string { return "index_meta" }

// IndexMailbox is one upstream folder.
type IndexMailbox struct {
	ID         string `gorm:"primaryKey;size:32"`
	Name       string `gorm:"column:name;uniqueIndex;not null"` // full IMAP name, decoded
	Delimiter  string `gorm:"size:4"`
	ParentID   string `gorm:"size:32;index"`
	Role       string `gorm:"size:16;index"` // JMAP role: inbox, sent, drafts, trash, junk, archive, all, flagged, important
	Attrs      string `gorm:"type:text"`     // JSON array of LIST attributes
	NoSelect   bool
	Subscribed bool
	SortOrder  int
	// UpstreamID is the OBJECTID MAILBOXID.
	UpstreamID string `gorm:"size:255;index"`
	// Hidden rows are folders the engine syncs that are not JMAP mailboxes
	// (Gmail's [Gmail]/All Mail, the source of the labels). Readers skip them and
	// their memberships.
	Hidden bool `gorm:"not null;default:false"`
	// Virtual rows have no IMAP folder of their own: their memberships are derived
	// from a source folder's messages (Gmail labels, the Archive mailbox), with
	// the source folder's UID. The engine never selects them.
	Virtual bool `gorm:"not null;default:false"`
	// Label is the Gmail label a virtual mailbox stands for, as X-GM-LABELS
	// shows it (\Inbox, \Sent, \Draft, Work/Projects); "" for Archive.
	Label string `gorm:"size:255"`
	// Sync point, committed with the index rows it covers.
	UIDValidity   uint32
	UIDNext       uint32
	HighestModSeq uint64
	// Last STATUS seen, for change detection.
	Messages uint32
	Unseen   uint32
	// SyncedAt is the last time the folder was compared; ChangedAt the last time it had changes.
	SyncedAt  *time.Time
	ChangedAt *time.Time
}

func (IndexMailbox) TableName() string { return "mailboxes" }

// IndexEmail is one message. Address and list fields are JSON.
type IndexEmail struct {
	ID       string `gorm:"primaryKey;size:32"`
	ThreadID string `gorm:"size:32;index;not null"`
	// UpstreamID is the stable upstream key: "oid:<EMAILID>", "guid:<X-GUID>" or "".
	UpstreamID       string `gorm:"size:300;index"`
	UpstreamThreadID string `gorm:"size:300;index"` // OBJECTID THREADID / X-GM-THRID
	MessageID        string `gorm:"size:998;index"` // without angle brackets
	InReplyTo        string `gorm:"type:text"`      // JSON []string
	References       string `gorm:"column:refs;type:text"`
	Subject          string `gorm:"type:text"`
	From             string `gorm:"column:from_addr;type:text"` // JSON [{name,email}]
	Sender           string `gorm:"type:text"`
	ReplyTo          string `gorm:"type:text"`
	To               string `gorm:"column:to_addr;type:text"`
	Cc               string `gorm:"type:text"`
	Bcc              string `gorm:"type:text"`
	SentAt           *time.Time
	ReceivedAt       time.Time `gorm:"index;not null"`
	Size             int64     `gorm:"not null"`
	Preview          string    `gorm:"type:text"`
	HasAttachment    bool      `gorm:"not null;default:false"`
	Keywords         string    `gorm:"type:text;not null"` // JSON object {"$seen":true}
	Seen             bool      `gorm:"not null;default:false"`
	Flagged          bool      `gorm:"not null;default:false"`
	Draft            bool      `gorm:"not null;default:false"`
	Answered         bool      `gorm:"not null;default:false"`
	BodyStructure    string    `gorm:"type:text"` // JSON imapc.BodyPart; paths are IMAP sections
	// OrphanedFrom is set while the email has lost its membership during a sync round
	// (it vanished from that mailbox) and may still be found elsewhere (an external
	// move, or a UIDVALIDITY reset). The round either re-attaches or destroys it, so
	// readers treat a non-empty value as "not found".
	OrphanedFrom string `gorm:"size:32;index"`
	// TrashLabels are the Gmail labels (JSON []string) an email had when the
	// gateway moved it to Trash, restored when it leaves Trash again.
	TrashLabels string `gorm:"type:text"`
	CreatedAt   time.Time
}

func (IndexEmail) TableName() string { return "emails" }

// IndexMembership places an email in a mailbox under a UID.
type IndexMembership struct {
	MailboxID string `gorm:"primaryKey;size:32"`
	UID       uint32 `gorm:"primaryKey;autoIncrement:false"`
	EmailID   string `gorm:"size:32;index;not null"`
	ModSeq    uint64
}

func (IndexMembership) TableName() string { return "memberships" }

// IndexEmailRef lists the Message-IDs an email references, so a parent that
// arrives after its reply still joins the reply's thread.
type IndexEmailRef struct {
	MessageID string `gorm:"primaryKey;size:998"`
	EmailID   string `gorm:"primaryKey;size:32;index"`
}

func (IndexEmailRef) TableName() string { return "email_refs" }

// IndexChange is one changelog entry.
type IndexChange struct {
	Seq       int64     `gorm:"primaryKey;autoIncrement:false;index:idx_changelog_type_seq,priority:2"`
	Type      string    `gorm:"size:16;not null;index:idx_changelog_type_seq,priority:1"`
	ObjectID  string    `gorm:"size:32;not null"`
	Kind      string    `gorm:"size:16;not null"` // created, updated, counts, destroyed
	CreatedAt time.Time `gorm:"index;not null"`
}

func (IndexChange) TableName() string { return "changelog" }
