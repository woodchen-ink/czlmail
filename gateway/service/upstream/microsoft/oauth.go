package microsoft

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Microsoft identity platform v2.0 endpoints. The "common" authority accepts work
// and school accounts of any tenant as well as personal Microsoft accounts, which
// matches the app registration "any organizational directory and personal
// Microsoft accounts".
const (
	AuthorityHost = "https://login.microsoftonline.com"
	AuthorizeURL  = AuthorityHost + "/common/oauth2/v2.0/authorize"
	TokenURL      = AuthorityHost + "/common/oauth2/v2.0/token"
	// AdminConsentURL is the tenant-wide consent endpoint. "organizations" lets the
	// admin sign in to their own tenant; personal accounts have no admin.
	AdminConsentURL = AuthorityHost + "/organizations/v2.0/adminconsent"
	// CallbackPath is the redirect URI path under the gateway's base URL; it must
	// be registered on the app as a Web platform redirect URI.
	CallbackPath = "/oauth/microsoft/callback"
)

// Scopes are the delegated permissions the gateway asks for. The resource scopes
// all belong to outlook.office.com: one access token serves IMAP and SMTP (Graph
// scopes would need a second token and are not used).
var Scopes = []string{
	"offline_access",
	"openid",
	"email",
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/SMTP.Send",
}

// resourceScopes need tenant consent; openid / email / offline_access do not.
var resourceScopes = Scopes[3:]

// SecretWarnBefore is how long before the client secret's expiry the admin
// overview starts reminding the operator to rotate it.
const SecretWarnBefore = 30 * 24 * time.Hour

// SecretMaxLifetime is the longest client secret Entra issues (24 months).
const SecretMaxLifetime = 2 * 365 * 24 * time.Hour

// SecretExpiry is the reminder state of the client secret.
type SecretExpiry string

// Secret expiry states.
const (
	SecretUnknown  SecretExpiry = "unknown" // no expiry date recorded
	SecretOK       SecretExpiry = "ok"
	SecretExpiring SecretExpiry = "expiring" // within SecretWarnBefore
	SecretExpired  SecretExpiry = "expired"
)

// SecretState tells where a client secret expiring at exp stands at now.
func SecretState(exp, now time.Time) SecretExpiry {
	switch {
	case exp.IsZero():
		return SecretUnknown
	case !now.Before(exp):
		return SecretExpired
	case exp.Sub(now) <= SecretWarnBefore:
		return SecretExpiring
	}
	return SecretOK
}

// AuthCodeParams are the Microsoft-specific authorization request parameters on
// top of the standard ones (client_id, redirect_uri, response_type=code, scope,
// state, PKCE). loginHint pre-fills the account on re-authorisation.
func AuthCodeParams(loginHint string) url.Values {
	v := url.Values{}
	v.Set("response_mode", "query")
	if loginHint != "" {
		v.Set("login_hint", loginHint)
	} else {
		// Let a browser signed in to several accounts pick the right one.
		v.Set("prompt", "select_account")
	}
	return v
}

// AdminConsentLink is the URL a tenant admin opens to consent for the whole
// organisation, for tenants where users may not consent to apps themselves
// (AADSTS65001 / 90094 "Need admin approval"). The answer comes back to the same
// callback with admin_consent=True (or error=...) and no code.
func AdminConsentLink(clientID, redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("scope", strings.Join(resourceScopes, " "))
	if state != "" {
		v.Set("state", state)
	}
	return AdminConsentURL + "?" + v.Encode()
}

// IDClaims are the id_token claims the gateway reads.
type IDClaims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	TenantID          string `json:"tid"`
	ObjectID          string `json:"oid"`
}

// Mailbox is the address to sign in to IMAP / SMTP with: the email claim when
// present, else preferred_username (the UPN, which for nearly every work account
// is the primary address).
func (c IDClaims) Mailbox() string {
	if c.Email != "" {
		return strings.ToLower(c.Email)
	}
	if strings.Contains(c.PreferredUsername, "@") {
		return strings.ToLower(c.PreferredUsername)
	}
	return ""
}

// PersonalTenant is the tenant id of personal Microsoft accounts.
const PersonalTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"

// Personal reports a personal Microsoft account (Outlook.com, Hotmail, Live).
func (c IDClaims) Personal() bool { return c.TenantID == PersonalTenant }

// ParseIDToken reads the claims of an id_token received directly from the token
// endpoint over TLS; per OpenID Connect Core 3.1.3.7 its signature need not be
// checked then. It must not be used on tokens from anywhere else.
func ParseIDToken(idToken string) (IDClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return IDClaims{}, errors.New("microsoft: malformed id_token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return IDClaims{}, errors.New("microsoft: malformed id_token payload")
	}
	var c IDClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return IDClaims{}, errors.New("microsoft: malformed id_token claims")
	}
	return c, nil
}
