package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func sessionWith(ids ...string) *jmap.Client {
	accs := map[jmap.ID]jmap.Account{}
	for _, id := range ids {
		accs[jmap.ID(id)] = jmap.Account{}
	}
	return &jmap.Client{Session: &jmap.Session{Accounts: accs}}
}

func cachedEmails(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM emails`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 首次登录 (没有归属记录) 不清、同一人重新登录不清、换人登录清空。
func TestEnsureCacheOwner(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertAccounts(ctx, tx, []store.Account{{ID: "c", Name: "wood"}}, nil); err != nil {
			return err
		}
		return store.UpsertEmails(ctx, tx, "c", []store.Email{{ID: "m1", Subject: "mine"}})
	}); err != nil {
		t.Fatal(err)
	}
	wood := config.Config{SessionEndpoint: "https://mail.example/jmap/session", Username: "wood@example"}
	other := config.Config{SessionEndpoint: wood.SessionEndpoint, Username: "appreview@example"}

	if err := a.ensureCacheOwner(ctx, st, wood, sessionWith("c")); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if cachedEmails(t, st) != 1 {
		t.Fatal("first sign-in of the cache's own user must keep the cache")
	}
	if err := a.ensureCacheOwner(ctx, st, wood, sessionWith("c")); err != nil || cachedEmails(t, st) != 1 {
		t.Fatalf("same user again: err=%v emails=%d", err, cachedEmails(t, st))
	}
	if err := a.ensureCacheOwner(ctx, st, other, sessionWith("u")); err != nil {
		t.Fatalf("different user: %v", err)
	}
	if cachedEmails(t, st) != 0 {
		t.Fatal("a different user must not see the previous user's cache")
	}
}
