package microsoft

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
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

// ClassifyTokenError maps a failed token request to an upstream error: a grant
// that only a new sign-in can replace is KindAuth (the upstream turns
// authRequired), throttling is KindThrottled, everything else is retried as
// KindUnreachable. A wrong or expired client secret (invalid_client, AADSTS7000215
// / 7000222) is also retried: it is the operator's app setting, affects every
// Microsoft mailbox alike and is fixed in the admin, not by the user signing in.
func ClassifyTokenError(status int, body []byte) *upstream.Error {
	te := ParseTokenError(status, body)
	return &upstream.Error{Kind: tokenKind(te), Op: "microsoft token", Err: te}
}

func tokenKind(te *TokenError) upstream.ErrorKind {
	switch {
	case te.Status == http.StatusTooManyRequests:
		return upstream.KindThrottled
	case te.Status >= 500:
		return upstream.KindUnreachable
	}
	switch te.Code {
	case "invalid_grant", "interaction_required", "consent_required", "login_required":
		return upstream.KindAuth
	case "invalid_client", "unauthorized_client", "invalid_scope", "temporarily_unavailable", "server_error":
		return upstream.KindUnreachable
	}
	if slices.Contains(grantGone, te.AADSTS()) {
		return upstream.KindAuth
	}
	return upstream.KindUnreachable
}
