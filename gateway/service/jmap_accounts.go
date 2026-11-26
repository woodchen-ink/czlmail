package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// UpstreamAccounts lists a gateway user's upstream mailboxes as JMAP accounts.
// No upstream exists yet, so every user gets an empty list and the Session
// answers 403 no-upstream; the upstream work packages fill this in.
type UpstreamAccounts struct{}

// UserSession implements jmapserver.AccountSource.
func (UpstreamAccounts) UserSession(_ context.Context, userID string) (*jmapserver.UserSession, error) {
	return &jmapserver.UserSession{Username: userID}, nil
}

// errNoUsers is returned while gateway users do not exist yet.
var errNoUsers = errors.New("gateway users are not implemented")

// NoUsers rejects every request until gateway users and app passwords exist.
type NoUsers struct{}

// Authenticate implements jmapserver.Authenticator.
func (NoUsers) Authenticate(*http.Request) (string, error) { return "", errNoUsers }
