package microsoft

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// TokenError is an error answer of the Microsoft identity platform token endpoint.
type TokenError struct {
	Status      int    // HTTP status
	Code        string `json:"error"`
	Description string `json:"error_description"`
	ErrorCodes  []int  `json:"error_codes"` // AADSTS numbers
	SubError    string `json:"suberror"`
}

func (e *TokenError) Error() string {
	desc, _, _ := strings.Cut(e.Description, "\r\n") // drop the Trace ID / Timestamp lines
	if desc == "" {
		return fmt.Sprintf("token endpoint: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("token endpoint: %s: %s", e.Code, desc)
}

// AADSTS is the first AADSTS number of the answer, 0 if none.
func (e *TokenError) AADSTS() int {
	if len(e.ErrorCodes) > 0 {
		return e.ErrorCodes[0]
	}
	if m := aadstsRe.FindStringSubmatch(e.Description); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

var aadstsRe = regexp.MustCompile(`AADSTS(\d+)`)

// grantGone are AADSTS numbers meaning the user must sign in again, whatever the
// error string says: the refresh token expired or was revoked, the password
// changed, MFA or a Conditional Access policy now requires interaction, consent
// was withdrawn, the account was disabled or deleted.
var grantGone = []int{
	50034,  // user not found in the directory
	50053,  // account locked
	50055,  // password expired
	50057,  // account disabled
	50076,  // MFA required
	50079,  // MFA registration required
	50126,  // invalid credentials
	50133,  // session invalid after a password change
	50158,  // external security challenge not satisfied
	50173,  // grant revoked (password change or reset)
	53003,  // blocked by Conditional Access
	54005,  // authorization code already redeemed
	65001,  // consent missing or withdrawn
	70000,  // invalid grant
	70008,  // code or refresh token expired
	70043,  // refresh token expired by sign-in frequency
	700082, // refresh token expired after inactivity
	700084, // refresh token lifetime exceeded
}

// ParseTokenError reads a non-200 token endpoint answer.
func ParseTokenError(status int, body []byte) *TokenError {
	te := &TokenError{Status: status}
	_ = json.Unmarshal(body, te)
	if te.Code == "" {
		te.Code = http.StatusText(status)
	}
	return te
}

// TokenClass is what a failed token request means for the upstream.
type TokenClass int

// Token error classes.
const (
	// TokenRetry: an outage or an answer the gateway cannot act on; retried.
	TokenRetry TokenClass = iota
	// TokenGrantGone: only a new sign-in by the user replaces the grant.
	TokenGrantGone
	// TokenBadClient: the app's client id or secret is wrong or the secret
	// expired (AADSTS7000215 / 7000222). Fixed in the gateway's OAuth app
	// settings, which restarts the mailboxes it stopped.
	TokenBadClient
	// TokenThrottled: HTTP 429.
	TokenThrottled
)

// Class sorts the answer: the OAuth error string first, then the AADSTS number
// for answers whose string is generic.
func (e *TokenError) Class() TokenClass {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return TokenThrottled
	case e.Status >= 500:
		return TokenRetry
	}
	switch e.Code {
	case "invalid_grant", "interaction_required", "consent_required", "login_required":
		return TokenGrantGone
	case "invalid_client", "unauthorized_client":
		return TokenBadClient
	case "temporarily_unavailable", "server_error":
		return TokenRetry
	}
	if slices.Contains(grantGone, e.AADSTS()) {
		return TokenGrantGone
	}
	return TokenRetry
}
