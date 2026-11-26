package service

import (
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
)

// OAuth errors.
var (
	ErrOAuthProviderNotFound = errors.New("oauth provider not found")
	ErrOAuthAppNotConfigured = errors.New("oauth app not configured")
	ErrInvalidClientID       = errors.New("invalid client id")
	ErrMissingClientSecret   = errors.New("missing client secret")
)

func init() {
	RegisterSealedColumn(SealedColumn{Table: "oauth_apps", IDColumn: "provider", Column: "client_secret_sealed", Purpose: crypto.PurposeOAuthClient})
}

func oauthAppAAD(provider string) crypto.AAD {
	return crypto.AAD{Table: "oauth_apps", RowID: provider, Column: "client_secret_sealed"}
}

// OAuthAppView is a provider's app settings, for the admin.
type OAuthAppView struct {
	Provider        string     `json:"provider"`
	Configured      bool       `json:"configured"`
	ClientID        string     `json:"clientId"`
	HasSecret       bool       `json:"hasSecret"`
	RedirectURI     string     `json:"redirectUri"`
	Scopes          []string   `json:"scopes"`
	Tenant          string     `json:"tenant,omitempty"`
	SecretExpiresAt *time.Time `json:"secretExpiresAt"`
	// SecretState is unknown / ok / expiring (within 30 days) / expired.
	SecretState oauth.SecretState `json:"secretState"`
	// AdminConsentURL is the tenant-wide consent link (Microsoft), once a client
	// id is stored.
	AdminConsentURL string     `json:"adminConsentUrl,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt"`
}

// OAuthAppInput changes a provider's app. A nil or empty ClientSecret keeps the
// stored one; a nil SecretExpiresAt clears it.
type OAuthAppInput struct {
	ClientID        string
	ClientSecret    *string
	Tenant          string
	SecretExpiresAt *time.Time
}

// RedirectURI is where a provider sends the browser back: <BASE_URL>/oauth/<name>/callback.
func (o *OAuth) RedirectURI(provider string) string {
	return o.baseURL + "/oauth/" + provider + "/callback"
}

// ListApps returns every provider's app settings (site admins only).
func (o *OAuth) ListApps(actor *model.User) ([]OAuthAppView, error) {
	if !IsAdmin(actor) {
		return nil, ErrForbidden
	}
	var rows []model.OAuthApp
	if err := o.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	names := o.providerNames()
	out := make([]OAuthAppView, 0, len(names))
	for _, name := range names {
		i := slices.IndexFunc(rows, func(r model.OAuthApp) bool { return r.Provider == name })
		var row *model.OAuthApp
		if i >= 0 {
			row = &rows[i]
		}
		out = append(out, o.appView(name, row))
	}
	return out, nil
}

func (o *OAuth) appView(name string, row *model.OAuthApp) OAuthAppView {
	p, _ := o.provider(name)
	v := OAuthAppView{Provider: name, RedirectURI: o.RedirectURI(name), Scopes: slices.Clone(p.Scopes)}
	if row != nil {
		v.ClientID, v.HasSecret, v.Tenant, v.SecretExpiresAt = row.ClientID, row.ClientSecretSealed != "", row.Tenant, row.SecretExpiresAt
		v.Configured = row.ClientID != "" && row.ClientSecretSealed != ""
		t := row.UpdatedAt
		v.UpdatedAt = &t
		if p.AdminConsentURL != nil && row.ClientID != "" {
			v.AdminConsentURL = p.AdminConsentURL(row.ClientID, v.RedirectURI, row.Tenant)
		}
	}
	v.SecretState = oauth.SecretStateAt(v.SecretExpiresAt, o.now())
	return v
}

// PutApp stores a provider's app settings (site admins only). The secret is
// required the first time.
func (o *OAuth) PutApp(actor *model.User, provider string, in OAuthAppInput) (*OAuthAppView, error) {
	if !IsAdmin(actor) {
		return nil, ErrForbidden
	}
	if _, ok := o.provider(provider); !ok {
		return nil, ErrOAuthProviderNotFound
	}
	id := strings.TrimSpace(in.ClientID)
	if id == "" || len(id) > 256 || strings.ContainsAny(id, " \t\r\n") {
		return nil, ErrInvalidClientID
	}
	var row model.OAuthApp
	err := o.db.Where("provider = ?", provider).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	row.Provider, row.ClientID, row.Tenant, row.SecretExpiresAt, row.UpdatedBy = provider, id, strings.TrimSpace(in.Tenant), in.SecretExpiresAt, actor.PublicID
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		if len(*in.ClientSecret) > 1024 {
			return nil, ErrMissingClientSecret
		}
		sealed, err := o.ring.Seal(crypto.PurposeOAuthClient, oauthAppAAD(provider), []byte(*in.ClientSecret))
		if err != nil {
			return nil, err
		}
		row.ClientSecretSealed = sealed
	}
	if row.ClientSecretSealed == "" {
		return nil, ErrMissingClientSecret
	}
	if err := o.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	o.appChanged(provider)
	v := o.appView(provider, &row)
	return &v, nil
}

// DeleteApp removes a provider's app. Its upstreams stay but cannot refresh
// tokens until an app is configured again.
func (o *OAuth) DeleteApp(actor *model.User, provider string) error {
	if !IsAdmin(actor) {
		return ErrForbidden
	}
	if _, ok := o.provider(provider); !ok {
		return ErrOAuthProviderNotFound
	}
	if err := o.db.Where("provider = ?", provider).Delete(&model.OAuthApp{}).Error; err != nil {
		return err
	}
	o.appChanged(provider)
	return nil
}

// client returns the provider with the stored app credentials.
func (o *OAuth) client(provider string) (*oauth.Client, error) {
	p, ok := o.provider(provider)
	if !ok {
		return nil, ErrOAuthProviderNotFound
	}
	var row model.OAuthApp
	err := o.db.Where("provider = ?", provider).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (row.ClientID == "" || row.ClientSecretSealed == "") {
		return nil, ErrOAuthAppNotConfigured
	}
	if err != nil {
		return nil, err
	}
	secret, err := o.ring.Open(crypto.PurposeOAuthClient, oauthAppAAD(provider), row.ClientSecretSealed)
	if err != nil {
		return nil, err
	}
	if p.WithTenant != nil {
		p = p.WithTenant(p.Clone(), row.Tenant)
	}
	return &oauth.Client{Provider: p, ClientID: row.ClientID, ClientSecret: string(secret), RedirectURL: o.RedirectURI(provider)}, nil
}
