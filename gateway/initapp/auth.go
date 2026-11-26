package initapp

import (
	"log/slog"
	"net/url"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

// Auth is the user, session and credential side of the gateway.
type Auth struct {
	Settings      *service.Settings
	Accounts      *service.Accounts
	Audit         *service.Audit
	Invalidations *service.Invalidations
	// Basic authenticates JMAP requests; subscribe to Invalidations to drop the
	// EventSource connections of users whose credentials were revoked.
	Basic *service.BasicAuthenticator
	Admin *handler.Admin
}

func newAuth(db *gorm.DB, cfg *config.Config, log *slog.Logger, ring *crypto.Keyring) (*Auth, error) {
	settings, err := service.LoadSettings(db, ring)
	if err != nil {
		return nil, err
	}
	limiter := service.NewRateLimiter()
	inval := service.NewInvalidations()
	accounts := service.NewAccounts(db, settings, limiter, inval, log)
	audit := service.NewAudit(db)
	return &Auth{
		Settings:      settings,
		Accounts:      accounts,
		Audit:         audit,
		Invalidations: inval,
		Basic:         service.NewBasicAuthenticator(db, limiter, inval, cfg.ClientIP, log),
		Admin: &handler.Admin{
			Accounts: accounts, Settings: settings, Audit: audit,
			ClientIP: cfg.ClientIP, SecureCookies: cfg.SecureCookies(), Log: log,
		},
	}, nil
}

// printSetupLink logs the one-time owner setup link while the gateway has no user.
// The link is valid for 30 minutes and only once; restart the gateway for a new one.
func printSetupLink(accounts *service.Accounts, baseURL string, log *slog.Logger) error {
	token, err := accounts.IssueSetupToken()
	if err != nil || token == "" {
		return err
	}
	link := baseURL + handler.AdminWebPrefix + "/setup/?token=" + url.QueryEscape(token)
	log.Warn("no user yet: open the setup link to create the owner account (valid 30 minutes, once; restart for a new link)",
		"setup_url", link, "expires_in", service.SetupTokenTTL.String())
	return nil
}
