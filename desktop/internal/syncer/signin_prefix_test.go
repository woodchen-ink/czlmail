package syncer

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 附加登录的同步器对外只说本机账号 id, 发给服务器时去掉前缀。
func TestSignInPrefixMapping(t *testing.T) {
	sess := &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"c": {IsPersonal: true}}}
	primary := &Syncer{client: &jmap.Client{Session: sess}}
	extra := &Syncer{client: &jmap.Client{Session: sess}}
	extra.SetSignIn("s2")

	if got := primary.Accounts(); !slices.Equal(got, []string{"c"}) {
		t.Errorf("primary accounts = %v", got)
	}
	if got := extra.Accounts(); !slices.Equal(got, []string{"s2~c"}) {
		t.Errorf("extra accounts = %v", got)
	}
	if extra.server("s2~c") != "c" || extra.sid("s2~c") != "c" || primary.server("c") != "c" {
		t.Error("server id should drop the prefix")
	}
	if !extra.owns("s2~c") || extra.owns("c") || extra.owns("s3~c") || !primary.owns("c") || primary.owns("s2~c") {
		t.Error("owns should compare prefixes")
	}
	if extra.server("c") != "" || primary.server("s2~c") != "" {
		t.Error("another sign-in's account must not map onto this session")
	}
	if !extra.isPersonal("s2~c") || primary.isPersonal("s2~c") {
		t.Error("isPersonal should look the account up by its server id")
	}
}

// 清理失去访问的账号时只看自己的账号: 别的登录的缓存不在这个会话里是正常的, 不能删。
func TestPruneAccountsIgnoresOtherSignIns(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, []store.Account{
			{ID: "c", IsPersonal: true}, {ID: "s2~c", IsPersonal: true}, {ID: "s2~e", Name: "shared"},
		}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{client: &jmap.Client{Session: &jmap.Session{Accounts: map[jmap.ID]jmap.Account{"c": {IsPersonal: true}}}}, store: st, log: slog.Default()}
	s.SetSignIn("s2")

	s.PruneAccounts(ctx)
	if got := s.PruneAccounts(ctx); !slices.Equal(got, []string{"s2~e"}) {
		t.Fatalf("removed %v, want [s2~e]", got)
	}
	accs, err := st.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range accs {
		ids = append(ids, a.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"c", "s2~c"}) {
		t.Fatalf("cached = %v, want [c s2~c]", ids)
	}
}
