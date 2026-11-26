package initapp

import (
	"net/http"

	"github.com/woodchen-ink/czlmail/gateway/service"
)

// jmapAuth adapts the Basic authenticator to jmapserver, naming the app password
// a request used so push subscriptions can be tied to it.
type jmapAuth struct{ *service.BasicAuthenticator }

// AuthenticateWithCredential implements jmapserver.CredentialAuthenticator.
func (a jmapAuth) AuthenticateWithCredential(r *http.Request) (string, string, error) {
	c, err := a.AuthenticateCredential(r)
	if err != nil {
		return "", "", err
	}
	return c.UserPublicID, c.AppPasswordID, nil
}
