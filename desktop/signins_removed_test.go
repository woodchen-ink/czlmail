package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 删除登录时可能还有一次连接在认证途中 (启动恢复、退避重试)。它回来时必须放弃:
// 不能把删掉的登录重新连上, 也不能把它的账号缓存写回去。
func TestConnectAfterRemoveSignInGivesUp(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	si := &signIn{id: "s2"}
	a := &App{ctx: ctx, log: slog.New(slog.NewTextHandler(io.Discard, nil)), store: st, ready: make(chan struct{})}
	a.markReady()
	a.extra = []*signIn{si}

	// 会话请求到达时 (认证进行中) 登录被删掉。
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		si.removed = true
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"capabilities":{"urn:ietf:params:jmap:core":{"maxSizeUpload":1000,"maxConcurrentUpload":1,"maxSizeRequest":1000,"maxConcurrentRequests":1,"maxCallsInRequest":16,"maxObjectsInGet":500,"maxObjectsInSet":500,"collationAlgorithms":[]},"urn:ietf:params:jmap:mail":{}},`+
			`"accounts":{"c":{"name":"me@b","isPersonal":true,"isReadOnly":false,"accountCapabilities":{"urn:ietf:params:jmap:mail":{}}}},`+
			`"primaryAccounts":{"urn:ietf:params:jmap:mail":"c"},"username":"me@b",`+
			`"apiUrl":"%[1]s/api","downloadUrl":"%[1]s/d/{accountId}/{blobId}/{name}?type={type}","uploadUrl":"%[1]s/u/{accountId}","eventSourceUrl":"%[1]s/es","state":"1"}`, srv.URL)
	}))
	defer srv.Close()

	cfg := config.Config{SessionEndpoint: srv.URL, AuthMethod: config.AuthPassword, Username: "me@b"}
	err = a.connect(si, cfg, basicAuthClient("me@b", "pw"), nil)
	if !errors.Is(err, errNotSignedIn) {
		t.Fatalf("connect after removal = %v, want errNotSignedIn", err)
	}

	a.mu.RLock()
	client, sync, cancel := si.client, si.sync, si.cancelSync
	a.mu.RUnlock()
	if client != nil || sync != nil || cancel != nil {
		t.Fatalf("removed sign-in got a session back: client=%v sync=%v", client != nil, sync != nil)
	}
	accs, err := st.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 0 {
		t.Fatalf("accounts written back for a removed sign-in: %+v", accs)
	}
}
