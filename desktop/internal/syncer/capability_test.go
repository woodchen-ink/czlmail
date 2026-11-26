package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
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

// TestNotesWithoutFileStorage 笔记库所在账号明确没有文件存储时报 2280 (服务器不支持), 会话没恢复时仍是离线 (2205)。
func TestNotesWithoutFileStorage(t *testing.T) {
	ctx := context.Background()
	var unsupported *capability.UnsupportedError
	if _, err := (&Syncer{client: &jmap.Client{Session: mailOnlySession()}}).FileSnapshot(ctx, "g"); !errors.As(err, &unsupported) {
		t.Errorf("mail-only account: err = %v, want 2280", err)
	}
	if _, err := (&Syncer{client: &jmap.Client{}}).FileSnapshot(ctx, "g"); !errors.Is(err, notes.ErrUnavailable) {
		t.Errorf("no session: err = %v, want notes.ErrUnavailable", err)
	}
}

// TestDropPIMWhenCapabilityGone 服务器不再公布日历/通讯录时, 那个账号缓存里的日程与联系人连同 state 一起清掉
// (日历不再画旧日程、提醒不再弹、联系人与补全不再列); 不在这个会话里的账号 (别的登录、会话还没恢复) 不动。
func TestDropPIMWhenCapabilityGone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	end := int64(1700003600)
	seed := func(acc string) {
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			if err := store.UpsertEvents(ctx, tx, acc, []store.EventRow{{ID: "e1", CalendarIDs: []string{"cal"}, Title: "old", StartAt: 1700000000, EndAt: &end, Raw: `{}`}}); err != nil {
				return err
			}
			if err := store.UpsertContacts(ctx, tx, acc, []store.ContactRow{{ID: "p1", DisplayName: "old", Raw: `{}`}}); err != nil {
				return err
			}
			if err := store.SetState(ctx, tx, acc, store.TypeCalendarEvent, "s1"); err != nil {
				return err
			}
			return store.SetState(ctx, tx, acc, store.TypeContactCard, "s1")
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("g")
	seed("other")

	s := New(&jmap.Client{Session: mailOnlySession()}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.mu.Lock()
	s.syncPIM(ctx, "g")
	s.syncPIM(ctx, "other")
	s.mu.Unlock()

	for _, table := range []string{store.TableEvents, store.TableContacts} {
		if empty, err := st.ObjectsEmpty(ctx, table, "g"); err != nil || !empty {
			t.Errorf("%s of the mail-only account not cleared (err %v)", table, err)
		}
		if empty, err := st.ObjectsEmpty(ctx, table, "other"); err != nil || empty {
			t.Errorf("%s of an account outside the session was cleared (err %v)", table, err)
		}
	}
	for _, dt := range []store.DataType{store.TypeCalendarEvent, store.TypeContactCard} {
		if state, _ := st.State(ctx, "g", dt); state != "" {
			t.Errorf("%s state kept (%q): a returning capability would not bootstrap again", dt, state)
		}
		if state, _ := st.State(ctx, "other", dt); state != "s1" {
			t.Errorf("%s state of an account outside the session = %q", dt, state)
		}
	}
}

// TestSessionSwapWhileReading 会话刷新持客户端锁换会话时, 能力检查同时在读 (go test -race 下不报竞争)。
func TestSessionSwapWhileReading(t *testing.T) {
	client := &jmap.Client{Session: mailOnlySession()}
	s := &Syncer{client: client}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			client.Lock()
			client.Session = mailOnlySession()
			client.Unlock()
		}
	}()
	for range 200 {
		s.Lacks("g", jmapx.CalendarsURI)
		s.accountSupports("g", jmapx.FileNodeURI)
		s.Accounts()
	}
	<-done
}
