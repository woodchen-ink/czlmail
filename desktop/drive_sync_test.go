package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 两台设备几乎同时写同一个同步文件: 读到之后网盘被别人写过, 这次写入被拒 (由 retryDriveSync 重读重合并), 不覆盖对方。
// 文件还不存在时两边都要新建, 后建的那个也被拒, 不会出现两个同名文件。
func TestDriveSyncFileWriteIsConditional(t *testing.T) {
	ts := newTwoServers(t)
	ts.oneA.SetCaps(append(slices.Clone(jmaptest.DefaultCaps), jmaptest.FileNode)...)
	ta := newTestApp(t)
	ta.signInPrimary(ts.one, "alice@one.test", "pw-alice")
	ta.waitSubjects("a", "inbox", "hello from one")

	const name = "conflict-test.json"
	open := func() *driveSyncFile {
		t.Helper()
		f, _, err := ta.a.openDriveSyncFile(ta.ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	// 都还没有文件: 先写的建出文件夹与文件, 后写的被拒。
	first, second := open(), open()
	if err := first.write(ta.ctx, []byte(`{"from":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if err := second.write(ta.ctx, []byte(`{"from":"second"}`)); !errors.Is(err, syncer.ErrFilesChanged) {
		t.Fatalf("second create = %v, want files changed", err)
	}

	// 已有文件: 读到之后被别人更新过, 这次更新被拒; 重读后能写。
	stale, fresh := open(), open()
	if err := fresh.write(ta.ctx, []byte(`{"from":"fresh"}`)); err != nil {
		t.Fatal(err)
	}
	if err := stale.write(ta.ctx, []byte(`{"from":"stale"}`)); !errors.Is(err, syncer.ErrFilesChanged) {
		t.Fatalf("stale update = %v, want files changed", err)
	}
	_, data, err := ta.a.openDriveSyncFile(ta.ctx, name)
	if err != nil || string(data) != `{"from":"fresh"}` {
		t.Fatalf("file = %q, %v; the stale write must not land", data, err)
	}

	// retryDriveSync 只重试网盘变了这一种失败。
	calls := 0
	err = retryDriveSync(func() error {
		calls++
		if calls < 2 {
			return syncer.ErrFilesChanged
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("retry: calls = %d, err = %v", calls, err)
	}
	calls = 0
	if err := retryDriveSync(func() error { calls++; return errNoDriveSync }); !errors.Is(err, errNoDriveSync) || calls != 1 {
		t.Fatalf("other errors must not retry: calls = %d, err = %v", calls, err)
	}
}
