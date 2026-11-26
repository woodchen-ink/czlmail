package microsoft

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

func TestNotConnectedRetriesThenAuthRequired(t *testing.T) {
	nc := &notConnected{}
	cfg := upstream.Config{AccountID: "a1"}
	nope := &imapc.StatusError{Command: "AUTHENTICATE", Status: "NO", Text: "User is authenticated but not connected."}
	for i := 1; i <= NotConnectedRetries; i++ {
		err := nc.classify(nope, cfg)
		var ue *upstream.Error
		if !errors.As(err, &ue) || ue.Code != CodeProbeIMAPDisabled {
			t.Fatalf("attempt %d: %v", i, err)
		}
		state, code := ue.Status()
		want := upstream.KindUnreachable
		if i == NotConnectedRetries {
			want = upstream.KindAuth
		}
		if ue.Kind != want || code != CodeProbeIMAPDisabled {
			t.Errorf("attempt %d: kind %v state %s code %d", i, ue.Kind, state, code)
		}
	}

	// A good sign-in starts the count again; other errors leave it alone.
	if err := nc.classify(nil, cfg); err != nil {
		t.Fatal(err)
	}
	other := &imapc.StatusError{Command: "AUTHENTICATE", Status: "NO", Text: "AUTHENTICATE failed."}
	if err := nc.classify(other, cfg); err != other {
		t.Errorf("other error changed: %v", err)
	}
	var ue *upstream.Error
	if err := nc.classify(nope, cfg); !errors.As(err, &ue) || ue.Kind != upstream.KindUnreachable {
		t.Errorf("after a good sign-in: %v", err)
	}
}

func TestNotConnectedLogsTokenOwner(t *testing.T) {
	var logs bytes.Buffer
	cfg := upstream.Config{AccountID: "a2", IMAP: upstream.Endpoint{Username: "robert@outlook.test"},
		Log: slog.New(slog.NewJSONHandler(&logs, nil)), TokenOwner: func() string { return "oid=o-1 email=zurn@outlook.test" }}
	(&notConnected{}).classify(&imapc.StatusError{Command: "AUTHENTICATE", Status: "NO", Text: "User is authenticated but not connected."}, cfg)
	out := logs.String()
	for _, want := range []string{`"msg":"microsoft imap not connected"`, `"account":"a2"`, `"user":"robert@outlook.test"`, `"token_account":"oid=o-1 email=zurn@outlook.test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
}
