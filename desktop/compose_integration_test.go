package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

func TestIntegrationDraftAndScheduled(t *testing.T) {
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
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := &App{ctx: ctx, log: log, store: st, client: client, sync: syncer.New(client, st, log)}
	a.cfg.Username = user
	_ = a.persistAccounts(ctx, client, st)
	acc := a.personalAccountID(st)
	if err := a.sync.SyncAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	ids, _ := a.ListIdentities(acc)
	if len(ids) == 0 {
		t.Skip("no identity")
	}
	req := ComposeRequest{AccountID: acc, IdentityID: ids[0].ID, To: []store.Address{{Email: user}},
		Subject: "czlmail-test 草稿", HTMLBody: "<p><b>草稿</b></p>", TextBody: "草稿", RequestReadReceipt: true}
	d1, err := a.SaveDraft(req)
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	req.DraftID = d1
	req.Subject = "czlmail-test 草稿 v2"
	d2, err := a.SaveDraft(req)
	if err != nil || d2 == d1 {
		t.Fatalf("resave: %q %v", d2, err)
	}
	if _, err := st.Email(ctx, acc, d1); err == nil {
		t.Error("old draft should be gone")
	}
	if err := a.DiscardDraft(acc, d2); err != nil {
		t.Fatalf("discard: %v", err)
	}
	t.Logf("maxDelayedSend=%d", a.MaxDelayedSend(acc))

	// 定时发送给自己, 延迟 2 小时, 随后取消, 验证服务器接受 HOLDFOR。
	sreq := ComposeRequest{AccountID: acc, IdentityID: ids[0].ID, To: []store.Address{{Email: user}},
		Subject: "czlmail-test 定时发送(应已取消)", TextBody: "如果你收到这封信, 说明取消没有生效。",
		SendAt: time.Now().Add(2 * time.Hour).Format(time.RFC3339)}
	if err := a.SendEmail(sreq); err != nil {
		t.Fatalf("scheduled send: %v", err)
	}
	list, err := a.ListScheduledSends(acc)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range list {
		if strings.HasPrefix(it.Subject, "czlmail-test 定时发送") {
			if err := a.CancelScheduledSend(acc, it.SubmissionID, it.EmailID); err != nil {
				t.Fatalf("cancel: %v", err)
			}
			n++
			_ = a.DiscardDraft(acc, it.EmailID)
		}
	}
	t.Logf("scheduled=%d canceled=%d", len(list), n)
	if n == 0 {
		t.Fatal("scheduled send not found")
	}
}
