package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 对账只删除服务器上已不存在的邮件, 不误删真实邮件。
func TestIntegrationReconcileEmails(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" {
		t.Skip()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	endpoint, _ := auth.SessionEndpoint(host)
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	_ = a.persistAccounts(ctx, client, st)
	acc := a.personalAccountID(st)
	if err := a.sync.SyncAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	before, _ := st.LocalEmailIDs(ctx, acc)

	fake := store.Email{ID: "czlmail-reconcile-fake", Subject: "fake", ReceivedAt: time.Now()}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error { return store.UpsertEmails(ctx, tx, acc, []store.Email{fake}) }); err != nil {
		t.Fatal(err)
	}
	removed, err := a.sync.ReconcileEmails(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := st.LocalEmailIDs(ctx, acc)
	t.Logf("local before=%d removed=%d after=%d", len(before), removed, len(after))
	if removed != 1 || len(after) != len(before) {
		t.Errorf("expected only the fake email removed")
	}
}
