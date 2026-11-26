package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestDiffAccounts(t *testing.T) {
	before := map[jmap.ID]jmap.Account{
		"c": {Name: "wood@czl.net", IsPersonal: true},
		"e": {Name: "support@czl.net"},
		"f": {Name: "finance@czl.net"},
	}
	after := map[jmap.ID]jmap.Account{
		"c": {Name: "wood@czl.net", IsPersonal: true},
		"e": {Name: "help@czl.net"},
		"s": {Name: "archive@czl.net"},
	}
	d := diffAccounts(before, after)
	if !slices.Equal(d.Added, []string{"s"}) || !slices.Equal(d.Removed, []string{"f"}) || !slices.Equal(d.Renamed, []string{"e"}) {
		t.Fatalf("diff = %+v", d)
	}
	if !diffAccounts(before, before).Empty() {
		t.Fatal("same session should give an empty diff")
	}
}

// TestDiffAccountsCapabilities 能力集合或能力对象变了的账号单独列出, 没变的不列。
func TestDiffAccountsCapabilities(t *testing.T) {
	mail := json.RawMessage(`{}`)
	before := map[jmap.ID]jmap.Account{
		"c": {Name: "me", IsPersonal: true, RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": mail}},
		"e": {Name: "support", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:submission": json.RawMessage(`{"maxDelayedSend":0}`)}},
		"f": {Name: "finance", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": mail}},
	}
	after := map[jmap.ID]jmap.Account{
		"c": {Name: "me", IsPersonal: true, RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": mail, "urn:ietf:params:jmap:calendars": mail}},
		"e": {Name: "support", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:submission": json.RawMessage(`{"maxDelayedSend":60}`)}},
		"f": {Name: "finance", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:mail": json.RawMessage(`{}`)}},
		// 同样的能力对象换了键顺序与空白, 不算变化。
		"g": {Name: "ops", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:submission": json.RawMessage(`{ "b":2, "a":1 }`)}},
	}
	before["g"] = jmap.Account{Name: "ops", RawCapabilities: map[jmap.URI]json.RawMessage{"urn:ietf:params:jmap:submission": json.RawMessage(`{"a":1,"b":2}`)}}
	d := diffAccounts(before, after)
	if !slices.Equal(d.Capabilities, []string{"c", "e"}) || len(d.Added)+len(d.Removed)+len(d.Renamed) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}

func TestHasPersonal(t *testing.T) {
	if hasPersonal(map[jmap.ID]jmap.Account{"e": {Name: "support"}}) {
		t.Fatal("no personal account expected")
	}
	if !hasPersonal(map[jmap.ID]jmap.Account{"c": {IsPersonal: true}}) {
		t.Fatal("personal account expected")
	}
}

// 会话里少了的账号要连续两次缺失才删缓存; 中途又出现就重新计数。
func TestPruneAccountsNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, []store.Account{{ID: "c", IsPersonal: true}, {ID: "s", Name: "archive"}}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	full := &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"c": {IsPersonal: true}, "s": {Name: "archive"}}}
	partial := &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"c": {IsPersonal: true}}}
	s := &Syncer{client: &jmap.Client{Session: partial}, store: st, log: slog.Default()}

	cached := func() []string {
		accs, err := st.Accounts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, a := range accs {
			ids = append(ids, a.ID)
		}
		slices.Sort(ids)
		return ids
	}

	if got := s.PruneAccounts(ctx); len(got) != 0 {
		t.Fatalf("first miss removed %v, want nothing", got)
	}
	// 又出现了: 计数清零, 下一次缺失仍只算第一次。
	s.client.Session = full
	s.PruneAccounts(ctx)
	s.client.Session = partial
	if got := s.PruneAccounts(ctx); len(got) != 0 {
		t.Fatalf("miss after reappearing removed %v, want nothing", got)
	}
	if got := cached(); !slices.Equal(got, []string{"c", "s"}) {
		t.Fatalf("cached = %v before confirmation", got)
	}
	if got := s.PruneAccounts(ctx); !slices.Equal(got, []string{"s"}) {
		t.Fatalf("second consecutive miss removed %v, want [s]", got)
	}
	if got := cached(); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("cached = %v after confirmation, want [c]", got)
	}
}
