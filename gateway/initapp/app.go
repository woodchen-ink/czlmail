package initapp

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/cron"
	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/router"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/mail"
	"github.com/woodchen-ink/czlmail/gateway/service/metrics"
	"github.com/woodchen-ink/czlmail/gateway/service/submission"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// cleanupEvery is how often expired sessions, audit rows and uploads are pruned.
const cleanupEvery = 6 * time.Hour

// App is the assembled gateway behind its HTTP handler. Serve and the protocol
// tests build it the same way, so tests exercise the real routes and wiring.
type App struct {
	Handler http.Handler
	Auth    *Auth
	JMAP    *jmapserver.Server
	Mail    *mail.Service
	// Upstreams manages users' upstream mailboxes; Manager runs them.
	Upstreams *service.Upstreams
	Manager   *service.UpstreamManager
	// Metrics counts upstream requests, rate-limit hits and push latency for the
	// overview trend; the upstream packages record through it.
	Metrics *metrics.Recorder
	// Overview shows the upstream cards from Upstreams and, once #43 sets its
	// Secrets source, the expiry of the Microsoft client secret.
	Overview *service.Overview
	Tasks    *cron.Scheduler
	// Submission sends mail (EmailSubmission, Identity).
	Submission *submission.Service
	// OAuth adds Gmail / Microsoft upstreams and keeps their tokens.
	OAuth *service.OAuth

	stop context.CancelFunc
}

// Options tune an App for tests; the zero value is production.
type Options struct {
	Submission submission.Options
}

// NewApp loads the keyring, wires the services and starts the background jobs.
// db must be open; Close stops the jobs and ends EventSource streams.
func NewApp(cfg *config.Config, db *gorm.DB, log *slog.Logger, version string) (*App, error) {
	return NewAppWith(cfg, db, log, version, Options{})
}

// NewAppWith is NewApp with test options.
func NewAppWith(cfg *config.Config, db *gorm.DB, log *slog.Logger, version string, opts Options) (*App, error) {
	ring, err := service.LoadKeyring(db, cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	auth, err := newAuth(db, cfg, log, ring)
	if err != nil {
		return nil, err
	}
	adminURL := cfg.BaseURL + "/admin/"
	mgr := service.NewUpstreamManager(db, ring, auth.Settings, cfg.DataDir, log, upstream.Timing{})
	gateway := jmapserver.GatewayInfo{Version: version, ProtocolVersion: 1, AdminURL: adminURL}
	jmap, err := jmapserver.New(jmapserver.Options{
		BaseURL:  cfg.BaseURL,
		DataDir:  cfg.DataDir,
		Auth:     jmapAuth{auth.Basic},
		Accounts: service.NewUpstreamAccounts(db, mgr, adminURL),
		Log:      log,
		TraceID:  middleware.TraceID,
	})
	if err != nil {
		return nil, err
	}
	jmap.RegisterCapability(jmapserver.GatewayCapability, gateway)
	// Server-level values are empty objects (RFC 8621 §1.3); the limits are per
	// account. The Mail methods come from service/mail, Submission from #41.
	jmap.RegisterCapability(service.MailCapability, struct{}{})
	jmap.RegisterCapability(service.SubmissionCapability, struct{}{})
	pushStore := service.NewPushStore(db, ring, log)
	if err := jmap.EnablePush(context.Background(), jmapserver.PushOptions{
		Store:                pushStore,
		AllowPrivateNetworks: func() bool { return auth.Settings.Bool(service.SettingAllowPrivateNetworks) },
	}); err != nil {
		return nil, err
	}
	cache, err := mail.NewCache(filepath.Join(cfg.DataDir, "cache", "mail"), mail.DefaultCacheBytes)
	if err != nil {
		return nil, err
	}
	mailSvc, err := mail.New(mail.Options{
		Upstreams: mgr,
		Log:       log,
		Uploads:   jmap.Uploads(),
		Cache:     cache,
		OnNewMail: newMailNotifier(jmap),
	})
	if err != nil {
		return nil, err
	}
	mailSvc.Register(jmap)
	mgr.OnDelivered(mailSvc.Delivered)
	mgr.OnRemove(cache.DropAccount)
	// Disabled user or revoked app password: drop the user's push streams (clients
	// with a still-valid app password reconnect at once) and the push subscriptions
	// of the revoked credentials, so a revoked device stops getting mail summaries.
	// A deleted user's upstreams stop and their index files go.
	auth.Invalidations.Subscribe(func(ev service.UserInvalidation) {
		jmap.EndUserStreams(ev.UserPublicID)
		if ev.Reason == "deleted" {
			go mgr.RemoveUser(ev.UserPublicID)
		}
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
	// Push connections steer polling: a client connecting syncs everything now,
	// none for 30 minutes slows polling down.
	jmap.ObserveStreams(mgr.SetClients)
	upstreams := service.NewUpstreams(db, ring, auth.Settings, mgr, log)
	auth.Admin.Upstreams = upstreams
	sending := submission.New(db, mgr, log, opts.Submission)
	sending.Register(jmap)
	auth.Admin.Submission = sending
	oauthSvc := service.NewOAuth(db, ring, upstreams, mgr, cfg.BaseURL, log)
	auth.Admin.OAuth = oauthSvc

	rec := metrics.New(db)
	tasks := cron.NewScheduler(log)
	tasks.Add(cron.PruneTask("admin_sessions", cleanupEvery, auth.Accounts.PruneSessions))
	tasks.Add(cron.PruneTask("audit_logs", cleanupEvery, auth.Audit.Prune))
	tasks.Add(cron.PruneTask("uploads", cleanupEvery, func() (int64, error) {
		n, err := jmap.Uploads().Cleanup(time.Now())
		return int64(n), err
	}))
	tasks.Add(cron.PruneTask("upstream_metrics", cleanupEvery, rec.Prune))
	tasks.Add(cron.Task{Name: "metrics_flush", Interval: metrics.FlushEvery, Run: func(context.Context) (string, error) {
		return "", rec.Flush()
	}})
	ctx, stop := context.WithCancel(context.Background())
	tasks.Add(cron.PruneTask("push_subscriptions", cleanupEvery, func() (int64, error) { return pushStore.PruneExpired(time.Now()) }))
	tasks.Add(cron.PruneTask("changelog", cleanupEvery, mgr.Trim))
	tasks.Add(cron.PruneTask("submissions", cleanupEvery, sending.Prune))
	mgr.SetMetrics(rec)
	if err := mgr.Run(ctx, jmap); err != nil {
		stop()
		return nil, err
	}
	tasks.Start(ctx)

	web, webInstalled := handler.NewAdminWeb(cfg.WebDir)
	if !webInstalled {
		log.Warn("admin web not found; /admin/ answers 503", "web_dir", cfg.WebDir)
	}
	system := service.NewSystem(db, service.SystemOptions{
		DataDir: cfg.DataDir, BaseURL: cfg.BaseURL, Version: version, Env: cfg.PublicEnv(),
		Streams: jmap.StreamCount, AdminWeb: webInstalled,
	})
	overview := &service.Overview{Upstreams: upstreams, Metrics: rec, System: system}
	auth.Admin.OverviewSvc, auth.Admin.SystemSvc, auth.Admin.Tasks = overview, system, tasks

	h := router.New(router.Deps{
		Log:      log,
		ClientIP: cfg.ClientIP,
		Health:   &handler.Health{DB: db, Version: version, Log: log},
		Admin:    auth.Admin,
		JMAP:     jmap,
		Czlmail:  &handler.CzlmailConfig{Gateway: gateway},
		AdminWeb: web,
	})
	return &App{Handler: h, Auth: auth, JMAP: jmap, Mail: mailSvc, Metrics: rec, Overview: overview, Tasks: tasks, Upstreams: upstreams, Manager: mgr, Submission: sending, OAuth: oauthSvc, stop: stop}, nil
}

// Close stops the background jobs and upstreams, writes the pending metrics and
// ends all EventSource streams. Call it before closing the database.
func (a *App) Close() {
	a.stop()
	a.Manager.Stop()
	_ = a.Metrics.Flush()
	a.JMAP.Close()
}
