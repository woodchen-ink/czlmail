package main

import (
	"bytes"
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

// 回复/转发带内嵌图片: 草稿存到服务器后, 读回的附件里应有带 cid 的内嵌部件。
func TestIntegrationInlineParts(t *testing.T) {
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

	png := badgeIcon(1)[22:]
	blobID, _, size, err := a.sync.UploadBlob(ctx, acc, bytes.NewReader(png))
	if err != nil || size == 0 {
		t.Fatalf("upload: %v", err)
	}
	draftID, err := a.SaveDraft(ComposeRequest{
		AccountID: acc, IdentityID: ids[0].ID, To: []store.Address{{Email: user}},
		Subject: "czlmail-test 内嵌图片", HTMLBody: `<p>图</p><img src="cid:inline-test@czlmail">`, TextBody: "图",
		Attachments: []AttachmentRef{{BlobID: blobID, Type: "image/png", Name: "i.png", CID: "inline-test@czlmail"}},
	})
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	defer a.DiscardDraft(acc, draftID)

	if err := a.sync.FetchBody(ctx, acc, draftID); err != nil {
		t.Fatal(err)
	}
	d, err := st.Email(ctx, acc, draftID)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, att := range d.Attachments {
		t.Logf("part: cid=%q inline=%v type=%s", att.CID, att.Inline, att.Type)
		if att.CID == "inline-test@czlmail" && att.Inline {
			ok = true
		}
	}
	if !ok {
		t.Error("inline part with cid not found")
	}
}
