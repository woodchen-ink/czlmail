package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// mailOnlySession 模拟只有邮件能力的服务器 (如经 JMAP 代理接入的 Gmail)。
func mailOnlySession() *jmap.Session {
	return &jmap.Session{Accounts: map[jmap.ID]jmap.Account{
		"g": {IsPersonal: true, RawCapabilities: map[jmap.URI]json.RawMessage{
			"urn:ietf:params:jmap:mail": json.RawMessage(`{}`),
		}},
	}}
}

// TestLacks 只有会话里的账号明确没公布能力才算不支持; 没有会话或不认识的账号照常放行。
func TestLacks(t *testing.T) {
	s := &Syncer{client: &jmap.Client{Session: mailOnlySession()}}
	if !s.Lacks("g", jmapx.CalendarsURI) || !s.Lacks("g", capability.SubmissionURI) {
		t.Error("mail-only account reported with calendars or submission")
	}
	if s.Lacks("g", "urn:ietf:params:jmap:mail") {
		t.Error("mail capability reported missing")
	}
	if s.Lacks("unknown", jmapx.CalendarsURI) {
		t.Error("account outside the session reported as lacking")
	}
	if (&Syncer{client: &jmap.Client{}}).Lacks("g", jmapx.CalendarsURI) {
		t.Error("no session yet, but reported as lacking: offline must not hide features")
	}
}

// TestUnsupportedWritesSendNothing 不支持的能力在发请求前就以 2280 拒绝 (客户端没有会话端点, 真发出去会是别的错误)。
func TestUnsupportedWritesSendNothing(t *testing.T) {
	s := &Syncer{client: &jmap.Client{Session: mailOnlySession()}}
	ctx := context.Background()
	var unsupported *capability.UnsupportedError
	for _, typ := range []string{jmapx.CalendarEvent, jmapx.ContactCard, jmapx.FileNode} {
		if _, err := s.SetPIM(ctx, "g", typ, map[string]any{"x": map[string]any{}}, nil, nil, nil); !errors.As(err, &unsupported) {
			t.Errorf("SetPIM %s: err = %v, want 2280", typ, err)
		}
	}
	if err := s.Send(ctx, Draft{AccountID: "g"}, "", ""); !errors.As(err, &unsupported) {
		t.Errorf("Send: err = %v, want 2280", err)
	}
	if ids, err := s.Identities(ctx, "g"); err != nil || ids != nil {
		t.Errorf("Identities = %v, %v; want none without a request", ids, err)
	}
	if list, err := s.ScheduledSends(ctx, "g"); err != nil || list != nil {
		t.Errorf("ScheduledSends = %v, %v; want none without a request", list, err)
	}
}
