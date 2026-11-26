package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

func TestPushStoreRoundTripAndRotation(t *testing.T) {
	db := openTestDB(t)
	ring, err := LoadKeyring(db, testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPushStore(db, ring)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	sub := &jmapserver.PushSubscription{
		ID: "Pabc", UserID: "usr1", CredentialID: "ap1", DeviceClientID: "dev",
		URL:    "https://connect.czl.net/api/push/p/secret-id",
		P256DH: make([]byte, 65), Auth: []byte("0123456789abcdef"),
		VerificationCode: "code", Expires: now.Add(time.Hour), Types: []string{},
		EmailPush: map[string]jmapserver.EmailPushConfig{"a1": {Filter: json.RawMessage(`{"inMailbox":"m1"}`), Properties: []string{"id"}, Urgency: "high"}},
		CreatedAt: now,
	}
	if err := store.CreatePush(ctx, sub); err != nil {
		t.Fatal(err)
	}
	plain := &jmapserver.PushSubscription{ID: "Pdef", UserID: "usr1", DeviceClientID: "web", URL: "https://example.com/p", Expires: now.Add(-time.Minute), CreatedAt: now}
	if err := store.CreatePush(ctx, plain); err != nil {
		t.Fatal(err)
	}

	// The URL and auth are not stored in the clear.
	var row model.PushSubscription
	db.First(&row, "public_id = ?", "Pabc")
	if strings.Contains(row.URL, "secret-id") || strings.Contains(row.Auth, "0123") || row.Auth == "" {
		t.Fatalf("stored in the clear: %+v", row)
	}

	list, err := store.ListPush(ctx, "usr1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list %v %v", list, err)
	}
	got := list[0]
	if got.URL != sub.URL || string(got.Auth) != string(sub.Auth) || got.Types == nil || len(got.Types) != 0 ||
		string(got.EmailPush["a1"].Filter) != `{"inMailbox":"m1"}` || got.CredentialID != "ap1" || !got.Expires.Equal(sub.Expires) {
		t.Fatalf("round trip %+v", got)
	}
	if list[1].Types != nil || list[1].P256DH != nil || list[1].EmailPush != nil {
		t.Fatalf("keyless round trip %+v", list[1])
	}

	got.Verified, got.Types = true, nil
	if err := store.UpdatePush(ctx, got); err != nil {
		t.Fatal(err)
	}
	if list, _ = store.ListPush(ctx, "usr1"); !list[0].Verified || list[0].Types != nil {
		t.Fatalf("update %+v", list[0])
	}

	users, _ := store.PushUsers(ctx)
	if !slices.Equal(users, []string{"usr1"}) {
		t.Fatalf("users %v", users)
	}

	// Both sealed columns follow a master key rotation.
	if err := db.AutoMigrate(&testSecret{}); err != nil { // registered globally by TestRotateMasterKey
		t.Fatal(err)
	}
	if _, err := RotateMasterKey(db, testKey(1), testKey(2)); err != nil {
		t.Fatal(err)
	}
	ring2, _ := LoadKeyring(db, testKey(2))
	store = NewPushStore(db, ring2)
	if list, err = store.ListPush(ctx, "usr1"); err != nil || list[0].URL != sub.URL || string(list[0].Auth) != string(sub.Auth) {
		t.Fatalf("after rotation %v %v", list, err)
	}

	if n, err := store.PruneExpired(now); err != nil || n != 1 {
		t.Fatalf("prune %d %v", n, err)
	}
	if err := store.DeletePush(ctx, "other", "Pabc"); err != nil {
		t.Fatal(err)
	}
	if list, _ = store.ListPush(ctx, "usr1"); len(list) != 1 {
		t.Fatal("deleted another user's subscription")
	}
}
