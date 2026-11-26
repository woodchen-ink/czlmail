package oauth

import "time"

// SecretWarnBefore is how long before a client secret expires that the admin is
// reminded to rotate it (Microsoft Entra secrets last at most 24 months).
const SecretWarnBefore = 30 * 24 * time.Hour

// SecretState is where a client secret stands.
type SecretState string

// Client secret states.
const (
	SecretUnknown  SecretState = "unknown" // no expiry date recorded
	SecretOK       SecretState = "ok"
	SecretExpiring SecretState = "expiring" // within SecretWarnBefore
	SecretExpired  SecretState = "expired"
)

// SecretStateAt tells where a client secret expiring at exp (nil: unknown)
// stands at now.
func SecretStateAt(exp *time.Time, now time.Time) SecretState {
	switch {
	case exp == nil || exp.IsZero():
		return SecretUnknown
	case !now.Before(*exp):
		return SecretExpired
	case exp.Sub(now) <= SecretWarnBefore:
		return SecretExpiring
	}
	return SecretOK
}

// AdminConsentState is the state parameter of admin consent links. The answer
// comes back to the provider's callback with admin_consent=True or error=...
// and no code; the callback tells it from the sign-in flow by this state.
const AdminConsentState = "admin-consent"
