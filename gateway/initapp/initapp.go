// Package initapp assembles the gateway: config, database, keyring, HTTP server.
package initapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

// EnvOldMasterKey carries the previous master key during `gateway rotate-key`.
const EnvOldMasterKey = "GATEWAY_OLD_MASTER_KEY"

// NewLogger returns the JSON logger used everywhere (level, time, msg, trace_id).
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// Serve runs the gateway until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, log *slog.Logger, version string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer database.Close(db)

	app, err := NewApp(cfg, db, log, version)
	if err != nil {
		return err
	}
	defer app.Close()
	if err := printSetupLink(app.Auth.Accounts, cfg.BaseURL, log); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: EventSource responses stay open for hours.
	}
	// Shutdown waits for idle connections, and an EventSource stream never idles.
	srv.RegisterOnShutdown(app.JMAP.Close)

	errCh := make(chan error, 1)
	go func() {
		log.Info("gateway started", "version", version, "listen", cfg.Listen, "base_url", cfg.BaseURL, "data_dir", cfg.DataDir)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// RotateKey re-encrypts all stored secrets from GATEWAY_OLD_MASTER_KEY to GATEWAY_MASTER_KEY.
// Run it with the gateway stopped, then start the gateway with the new key only.
func RotateKey(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	oldKey, err := config.ParseMasterKey(EnvOldMasterKey, os.Getenv(EnvOldMasterKey))
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer database.Close(db)

	n, err := service.RotateMasterKey(db, oldKey, cfg.MasterKey)
	if err != nil {
		return fmt.Errorf("rotate master key: %w", err)
	}
	log.Info("master key rotated", "values", n)
	return nil
}
