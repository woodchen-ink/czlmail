package syncer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 停用的同步器不再发请求; 停用前已发出、停用后才回来的请求不写库。
func TestStoppedSyncerNeitherCallsNorWrites(t *testing.T) {
	srv := jmaptest.New(t)
	u := srv.AddUser("me@x.test", "pw")
	acc := srv.AddAccount("a", "me@x.test")
	u.Grant(acc, true)
	acc.AddEmail(jmaptest.Email{Subject: "hello"})

	client := &jmap.Client{SessionEndpoint: srv.SessionURL()}
	client.WithBasicAuth("me@x.test", "pw")
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(client, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 引导卡在服务器上时停用; 回来的结果不能落库。
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv.OnMethod(func(c jmaptest.Call) {
		if c.Method == "Email/query" {
			once.Do(func() { close(blocked); <-release })
		}
	})
	done := make(chan error, 1)
	go func() { done <- s.SyncMail(ctx, "a") }()
	<-blocked
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for a request, not just for writes")
	}
	close(release)
	if err := <-done; !errors.Is(err, errStopped) {
		t.Fatalf("SyncMail after Close = %v, want 1106", err)
	}
	if list, _ := st.EmailsByMailbox(ctx, "a", "inbox", 10, 0); len(list) != 0 {
		t.Fatalf("stopped syncer wrote %d emails", len(list))
	}

	srv.ResetCalls()
	if err := s.MarkRead(ctx, "a", []string{"m1"}, true); !errors.Is(err, errStopped) {
		t.Fatalf("MarkRead after Close = %v", err)
	}
	if calls := srv.Calls(); len(calls) != 0 {
		t.Fatalf("stopped syncer sent %d requests", len(calls))
	}
}
