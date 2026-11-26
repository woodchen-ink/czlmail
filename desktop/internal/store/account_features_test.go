package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
)

// TestAccountFeatures 账号行存下的能力读出来就是功能开关; 旧版存的 URI 数组照认, 没存过的当作全部支持。
func TestAccountFeatures(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	caps := map[string]map[jmap.URI]json.RawMessage{
		"c": {"urn:ietf:params:jmap:mail": json.RawMessage(`{}`), "urn:ietf:params:jmap:submission": json.RawMessage(`{"maxDelayedSend":120}`),
			"urn:ietf:params:jmap:calendars": json.RawMessage(`{}`)},
		"s2~g": {"urn:ietf:params:jmap:mail": json.RawMessage(`{}`)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertAccounts(ctx, tx, []Account{{ID: "c", IsPersonal: true}, {ID: "s2~g", IsPersonal: true}, {ID: "x"}}, caps)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO accounts (id, name, account_capabilities, updated_at) VALUES ('old', '', '["urn:ietf:params:jmap:mail","urn:ietf:params:jmap:sieve"]', 0)`); err != nil {
		t.Fatal(err)
	}

	list, err := s.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]capability.Features{}
	for _, a := range list {
		got[a.ID] = a.Features
	}
	want := map[string]capability.Features{
		"c":    {Submission: true, MaxDelayedSend: 120, Calendars: true},
		"s2~g": {},
		"x":    capability.All(),
		"old":  {Sieve: true},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: features = %+v, want %+v", id, got[id], w)
		}
	}

}

// TestRemoveSignInDropsFeatures 移除附加登录时它的账号行连同能力一起删掉, 其余登录的能力不动。
func TestRemoveSignInDropsFeatures(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	caps := map[string]map[jmap.URI]json.RawMessage{
		"c":    {"urn:ietf:params:jmap:mail": json.RawMessage(`{}`), "urn:ietf:params:jmap:calendars": json.RawMessage(`{}`)},
		"s2~c": {"urn:ietf:params:jmap:mail": json.RawMessage(`{}`)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return UpsertAccounts(ctx, tx, []Account{{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}}, caps)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSignInData(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE id = 's2~c'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("removed sign-in still has %d account rows (err %v)", n, err)
	}
	list, err := s.Accounts(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "c" || !list[0].Features.Calendars {
		t.Fatalf("remaining accounts = %+v, %v", list, err)
	}
}
