package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

func TestIntegrationDirectory(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" {
		t.Skip()
	}
	ctx := context.Background()
	endpoint, _ := auth.SessionEndpoint(host)
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	_ = a.persistAccounts(ctx, client, st)
	got, err := a.SearchDirectory("zn")
	t.Logf("directory size=%d result=%v err=%v", len(a.directory()), got, err)
	if len(got) == 0 {
		t.Error("expected zn in directory")
	}
}
