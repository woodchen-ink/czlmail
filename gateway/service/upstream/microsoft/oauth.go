package microsoft

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
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
	// profile adds oid and preferred_username to the id_token (the account's
	// stable id and sign-in name); without it a personal account's id_token
	// has only email.
	"profile",
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/SMTP.Send",
}

// resourceScopes need tenant consent; the OpenID ones do not.
var resourceScopes = Scopes[4:]

// AdminConsentLink is the URL a tenant admin opens to consent for the whole
// organisation, for tenants where users may not consent to apps themselves
// (AADSTS65001 / 90094 "Need admin approval"). tenant is the app's tenant
// setting; for the multi-tenant authorities the admin signs in to their own
// organisation.
func AdminConsentLink(clientID, redirectURI, tenant, state string) string {
	endpoint := AdminConsentURL
	switch strings.ToLower(tenant) {
	case "", "common", "organizations", "consumers":
	default:
		endpoint = AuthorityHost + "/" + url.PathEscape(tenant) + "/v2.0/adminconsent"
	}
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("scope", strings.Join(resourceScopes, " "))
	if state != "" {
		v.Set("state", state)
	}
	return endpoint + "?" + v.Encode()
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

// consumerDomains host Outlook.com mailboxes: a personal account that signs in
// with one of them has its mailbox at that address.
var consumerDomains = []string{"outlook.", "hotmail.", "live.", "msn.com", "windowslive.com", "passport.com"}

// ConsumerAddress reports an address at an Outlook.com domain
// (outlook.com, outlook.de, hotmail.co.uk, live.cn, msn.com, ...).
func ConsumerAddress(addr string) bool {
	_, domain, ok := strings.Cut(strings.ToLower(addr), "@")
	if !ok {
		return false
	}
	for _, d := range consumerDomains {
		if domain == d {
			return true
		}
		if rest, ok := strings.CutPrefix(domain, d); ok && strings.HasSuffix(d, ".") && countryTLD(rest) {
			return true
		}
	}
	return false
}

// countryTLD: "com", "de", "co.uk", "com.au" — at most two short labels.
func countryTLD(s string) bool {
	labels := strings.Split(s, ".")
	if len(labels) > 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 3 {
			return false
		}
	}
	return true
}

var generatedRe = regexp.MustCompile(`(?i)^outlook_[0-9a-f]{8,32}@outlook\.com$`)

// GeneratedAddress reports the address Outlook.com gives the mailbox of a
// personal account registered with another provider's address
// (outlook_<hex>@outlook.com). It is not an alias of the account, so neither
// the id_token nor Graph /me shows it; sending From the sign-in address is
// refused (SendAsDenied).
func GeneratedAddress(addr string) bool { return generatedRe.MatchString(addr) }

// MailboxAddress is the mailbox's own address for a sign-in name. Work and
// school accounts and personal accounts at an Outlook.com domain have their
// mailbox at the sign-in address (the email claim is the primary SMTP
// address). A personal account signed in with another provider's address has
// an outlook_<hex>@outlook.com mailbox that only its mail shows: confirmed is
// false and the sign-in name stands in until it is found.
func MailboxAddress(c IDClaims, login string) (address string, confirmed bool) {
	if !c.Personal() || ConsumerAddress(login) {
		return login, true
	}
	return login, false
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

// Endpoints are the authorize and token URLs for the app's tenant setting: ""
// or "common" for every account type, "organizations" for work accounts only,
// "consumers" for personal ones, or one tenant (id or domain) for a single
// organisation's app.
func Endpoints(tenant string) (authorize, token string) {
	if tenant == "" {
		tenant = "common"
	}
	base := AuthorityHost + "/" + url.PathEscape(tenant) + "/oauth2/v2.0"
	return base + "/authorize", base + "/token"
}
