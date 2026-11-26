package model

import "time"

// Upstream kinds.
const (
	UpstreamKindIMAP      = "imap"
	UpstreamKindGmail     = "gmail"
	UpstreamKindMicrosoft = "microsoft"
)

// Proxy modes of an upstream.
const (
	ProxyFollow = "follow" // use the global default (setting proxy.default)
	ProxyNone   = "none"   // always connect directly
	ProxyCustom = "custom" // ProxyURL + ProxyPasswordSealed
)

// Upstream is one mailbox a user connected. PublicID is its JMAP accountId; it
// stays the same for the upstream's whole life (also across re-authorisation).
// Its index lives in data/upstreams/<PublicID>.sqlite.
type Upstream struct {
	ID       uint   `gorm:"primaryKey"`
	PublicID string `gorm:"size:32;uniqueIndex;not null"`
	UserID   uint   `gorm:"not null;index:idx_upstreams_user,priority:1"`
	Kind     string `gorm:"size:16;not null"`
	Name     string `gorm:"size:64;not null"`  // shown as the account name
	Email    string `gorm:"size:254;not null"` // the mailbox address

	IMAPHost     string `gorm:"column:imap_host;size:253"`
	IMAPPort     int    `gorm:"column:imap_port"`
	IMAPTLS      string `gorm:"column:imap_tls;size:16"` // tls, starttls, plain
	IMAPUsername string `gorm:"column:imap_username;size:254"`
	SMTPHost     string `gorm:"column:smtp_host;size:253"`
	SMTPPort     int    `gorm:"column:smtp_port"`
	SMTPTLS      string `gorm:"column:smtp_tls;size:16"`
	SMTPUsername string `gorm:"column:smtp_username;size:254"` // empty: same as IMAP

	// Sealed with crypto.PurposeUpstreamSecret, AAD (upstreams, public_id, column).
	PasswordSealed     string `gorm:"column:password_sealed;type:text"`
	SMTPPasswordSealed string `gorm:"column:smtp_password_sealed;type:text"` // empty: same as IMAP
	// OAuthTokenSealed holds the OAuth token set of gmail / microsoft upstreams (#42 / #43).
	OAuthTokenSealed string `gorm:"column:oauth_token_sealed;type:text"`

	ProxyMode           string `gorm:"size:16;not null;default:follow"`
	ProxyURL            string `gorm:"size:300"`  // socks5://[user@]host:port, without password
	ProxyPasswordSealed string `gorm:"type:text"` // crypto.PurposeProxySecret

	Paused    bool `gorm:"not null;default:false"`
	IsDefault bool `gorm:"not null;default:false;index:idx_upstreams_user,priority:2"`
	// AddressPending: Email is only the OAuth sign-in name (IMAPUsername) and
	// the mailbox's own address is not known yet. The gateway looks for it in
	// the synced mail; setting Email in the admin clears it.
	AddressPending bool `gorm:"not null;default:false"`

	// Status is the last jmapserver upstream status, kept across restarts.
	Status      string `gorm:"size:16;not null;default:syncing"`
	StatusCode  int
	StatusSince time.Time
	// LastError is the upstream's own error text, for the admin only.
	LastError string `gorm:"type:text"`
	// ServerCaps is the JSON upstream.Capabilities seen at the last connection test
	// or connection, for the admin and the Session's sort options.
	ServerCaps string `gorm:"type:text"`
	// SentCopy is how sent mail reaches the Sent folder: auto (not known yet),
	// gateway (the gateway files the draft) or server (the server saves its own copy).
	SentCopy string `gorm:"size:16;not null;default:auto"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Upstream) TableName() string { return "upstreams" }
