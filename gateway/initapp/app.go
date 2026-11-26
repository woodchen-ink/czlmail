package initapp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/cron"
	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/router"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

// cleanupEvery is how often expired sessions, audit rows and uploads are pruned.
const cleanupEvery = 6 * time.Hour

// App is the assembled gateway behind its HTTP handler. Serve and the protocol
// tests build it the same way, so tests exercise the real routes and wiring.
type App struct {
	Handler http.Handler
	Auth    *Auth
	JMAP    *jmapserver.Server

	stop context.CancelFunc
}

// NewApp loads the keyring, wires the services and starts the background jobs.
// db must be open; Close stops the jobs and ends EventSource streams.
func NewApp(cfg *config.Config, db *gorm.DB, log *slog.Logger, version string) (*App, error) {
	ring, err := service.LoadKeyring(db, cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	auth, err := newAuth(db, cfg, log)
	if err != nil {
		return nil, err
	}
	gateway := jmapserver.GatewayInfo{Version: version, ProtocolVersion: 1, AdminURL: cfg.BaseURL + "/admin/"}
	jmap, err := jmapserver.New(jmapserver.Options{
		BaseURL:  cfg.BaseURL,
		DataDir:  cfg.DataDir,
		Auth:     jmapAuth{auth.Basic},
		Accounts: service.NewUpstreamAccounts(db),
		Log:      log,
		TraceID:  middleware.TraceID,
	})
	if err != nil {
		return nil, err
	}
	jmap.RegisterCapability(jmapserver.GatewayCapability, gateway)
	pushStore := service.NewPushStore(db, ring)
	if err := jmap.EnablePush(context.Background(), jmapserver.PushOptions{
		Store:                pushStore,
		AllowPrivateNetworks: func() bool { return auth.Settings.Bool(service.SettingAllowPrivateNetworks) },
	}); err != nil {
		return nil, err
	}
	// Disabled user or revoked app password: drop the user's push streams (clients
	// with a still-valid app password reconnect at once) and the push subscriptions
	// of the revoked credentials, so a revoked device stops getting mail summaries.
	auth.Invalidations.Subscribe(func(ev service.UserInvalidation) {
		jmap.EndUserStreams(ev.UserPublicID)
		if ev.Reason == "email_changed" {
			return // same credentials, same mailboxes
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := jmap.DropPushSubscriptions(ctx, ev.UserPublicID, ev.AppPasswordID); err != nil {
				log.Error("drop push subscriptions", "user", ev.UserPublicID, "err", err)
			}
		}()
	})

	ctx, stop := context.WithCancel(context.Background())
	cron.StartCleanup(ctx, log, cleanupEvery, map[string]cron.Pruner{
		"admin_sessions": auth.Accounts.PruneSessions,
		"audit_logs":     auth.Audit.Prune,
		"uploads": func() (int64, error) {
			n, err := jmap.Uploads().Cleanup(time.Now())
			return int64(n), err
		},
		"push_subscriptions": func() (int64, error) { return pushStore.PruneExpired(time.Now()) },
	})

	h := router.New(router.Deps{
		Log:      log,
		ClientIP: cfg.ClientIP,
		Health:   &handler.Health{DB: db, Version: version, Log: log},
		Admin:    auth.Admin,
		JMAP:     jmap,
		Czlmail:  &handler.CzlmailConfig{Gateway: gateway},
	})
	return &App{Handler: h, Auth: auth, JMAP: jmap, stop: stop}, nil
}

// Close stops the background jobs and ends all EventSource streams.
func (a *App) Close() {
	a.stop()
	a.JMAP.Close()
}
