package model

import "time"

// OAuthApp is the OAuth client a site admin registered with a provider (Google
// Cloud, Microsoft Entra). One row per provider; without it that provider's
// mailboxes cannot be added.
type OAuthApp struct {
	Provider string `gorm:"primaryKey;size:16"` // oauth.Provider.Name
	ClientID string `gorm:"size:256;not null"`
	// ClientSecretSealed: crypto.PurposeOAuthClient, AAD (oauth_apps, provider, client_secret_sealed).
	ClientSecretSealed string `gorm:"type:text"`
	// Tenant is Microsoft's directory ("common", "organizations" or a tenant id).
	Tenant string `gorm:"size:64"`
	// SecretExpiresAt is when a Microsoft client secret stops working (they
	// expire after at most 24 months); the admin is warned before.
	SecretExpiresAt *time.Time
	UpdatedBy       string `gorm:"size:32"` // user public id
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (OAuthApp) TableName() string { return "oauth_apps" }
