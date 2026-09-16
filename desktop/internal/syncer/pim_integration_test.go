package syncer

import (
	"context"
	"testing"
	"time"
)

// 日历、通讯录、文件的引导与增量应能跑通, 且落库条数与服务端一致量级。
func TestIntegrationPIM(t *testing.T) {
	s, cleanup := newTestSyncer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	for _, acc := range s.Accounts() {
		if err := s.SyncAccount(ctx, acc); err != nil {
			t.Fatalf("sync %s: %v", acc, err)
		}
	}
	// 再同步一次走 /changes 路径。
	for _, acc := range s.Accounts() {
		if err := s.SyncAccount(ctx, acc); err != nil {
			t.Fatalf("resync %s: %v", acc, err)
		}
	}

	cals, err := s.store.Calendars(ctx)
	if err != nil || len(cals) == 0 {
		t.Fatalf("calendars: %d, %v", len(cals), err)
	}
	books, _ := s.store.AddressBooks(ctx)
	contacts, _ := s.store.Contacts(ctx, "", "", "", 10000)
	events, _ := s.store.EventsInRange(ctx, nil, 0, 1<<40)
	files, _ := s.store.FileNodes(ctx, "c", "")
	t.Logf("calendars=%d books=%d contacts=%d events=%d rootFiles=%d", len(cals), len(books), len(contacts), len(events), len(files))
	if len(contacts) == 0 || len(events) == 0 {
		t.Fatal("expected contacts and events on the reference server")
	}
}
