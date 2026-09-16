package main

import (
	"context"
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

func TestIntegrationFiltersVacation(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" {
		t.Skip()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	endpoint, _ := auth.SessionEndpoint(host)
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "f.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}

	f, err := a.GetFilters("c")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("managed=%v rules=%d", f.Managed, len(f.Rules))
	if f.Managed {
		// 原样写回: 规则不变, 验证生成、校验、保存整条链路。
		if err := a.SaveFilters("c", f.Rules); err != nil {
			t.Fatalf("save filters: %v", err)
		}
		g, _ := a.GetFilters("c")
		if len(g.Rules) != len(f.Rules) {
			t.Fatalf("rules changed: %d → %d", len(f.Rules), len(g.Rules))
		}
	}
	if err := a.SaveSieveScript("c", "if header :contains \"From\" { broken"); err == nil {
		t.Error("invalid script must be rejected before saving")
	}

	v, err := a.GetVacation("c")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SaveVacation("c", v); err != nil {
		t.Fatalf("save vacation: %v", err)
	}
	t.Logf("vacation=%+v", v)
}

func TestIntegrationMailboxAdmin(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" {
		t.Skip()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	endpoint, _ := auth.SessionEndpoint(host)
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	if err := a.sync.SyncAccount(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	find := func(name string) string {
		boxes, _ := st.Mailboxes(ctx, "c")
		for _, b := range boxes {
			if b.Name == name {
				return b.ID
			}
		}
		return ""
	}
	if err := a.CreateMailbox("c", "", "czlmail-test-folder"); err != nil {
		t.Fatalf("create: %v", err)
	}
	parent := find("czlmail-test-folder")
	if parent == "" {
		t.Fatal("folder not synced")
	}
	defer a.DeleteMailbox("c", parent, false)
	if err := a.CreateMailbox("c", parent, "child"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	child := find("child")
	if err := a.RenameMailbox("c", child, "czlmail-test-child"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := a.MoveMailbox("c", child, ""); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := a.DeleteMailbox("c", child, false); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if find("czlmail-test-child") != "" {
		t.Error("child still present")
	}
}
