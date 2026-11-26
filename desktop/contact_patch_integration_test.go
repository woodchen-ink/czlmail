package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 在真实服务器上验证: 保存联系人时条目原 key 与编辑器不管的属性不丢。
// 卡片由其它客户端的写法直接建 (带 pref、多个 context / feature、links 的 kind 与 mediaType、countryCode、第二个组织),
// 再经编辑器的读写路径 (GetContact → SaveContact) 保存两次, 比较服务器上的原文。
func TestIntegrationContactPatch(t *testing.T) {
	host, user, pass := os.Getenv("CZLMAIL_TEST_HOST"), os.Getenv("CZLMAIL_TEST_USER"), os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" || user == "" || pass == "" {
		t.Skip("set CZLMAIL_TEST_HOST / _USER / _PASS to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	endpoint, err := auth.SessionEndpoint(host)
	if err != nil {
		t.Fatal(err)
	}
	client := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := client.Authenticate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "it.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := sessionTestApp(ctx, log, st, client)
	acc := string(client.Session.PrimaryAccounts[jmap.URI("urn:ietf:params:jmap:contacts")])
	if err := a.primary.sync.SyncAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	books, _ := a.ListAddressBooks()
	bookID := ""
	for _, b := range books {
		if b.AccountID == acc && b.IsDefault {
			bookID = b.ID
		}
	}
	if bookID == "" {
		t.Skip("no default address book")
	}

	card := map[string]any{
		"@type": "Card", "version": "1.0", "kind": "individual", "uid": "urn:uuid:" + newUUID(),
		"addressBookIds": map[string]bool{bookID: true},
		"name": map[string]any{"isOrdered": true, "components": []map[string]string{
			{"kind": "given", "value": "Jana"}, {"kind": "surname", "value": "czlmail-test Nováková"}}},
		"organizations": map[string]any{"org1": map[string]any{"name": "ACME"}, "org2": map[string]any{"name": "Second Org"}},
		"emails": map[string]any{
			"mainMail": map[string]any{"address": "czlmail-test-jana@example.com", "contexts": map[string]bool{"work": true, "private": true}, "pref": 1, "label": "main"},
			"homeMail": map[string]any{"address": "czlmail-test-home@example.com", "contexts": map[string]bool{"private": true}},
		},
		"phones": map[string]any{"cell": map[string]any{"number": "+420 600 000 000", "features": map[string]bool{"mobile": true, "text": true}, "pref": 2}},
		"addresses": map[string]any{"office": map[string]any{
			"isOrdered": true, "countryCode": "CZ", "contexts": map[string]bool{"work": true},
			"components": []map[string]string{{"kind": "name", "value": "Main"}, {"kind": "number", "value": "1"}, {"kind": "locality", "value": "Praha"}},
		}},
		"links": map[string]any{"site": map[string]any{"uri": "https://example.com/jana", "kind": "contact", "mediaType": "text/html"}},
	}
	created, err := a.primary.sync.SetPIM(ctx, acc, jmapx.ContactCard, map[string]any{"new": card}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created["new"]
	defer a.DeleteContacts(acc, []string{id})

	server := func() map[string]json.RawMessage {
		t.Helper()
		if err := a.primary.sync.SyncAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
		raw, err := st.Contact(ctx, acc, id)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	before := server()
	t.Logf("as created: %s", mustJSON(before))

	// 1. 打开后原样保存: 除 updated 外服务器上什么都不变。
	f, err := a.GetContact(acc, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveContact(*f); err != nil {
		t.Fatalf("save unchanged: %v", err)
	}
	after := server()
	for _, k := range []string{"name", "organizations", "emails", "phones", "addresses", "links"} {
		if string(before[k]) != string(after[k]) {
			t.Errorf("unchanged save altered %s:\n before %s\n after  %s", k, before[k], after[k])
		}
	}

	// 2. 改一个邮箱的标签、加一个邮箱、改网站地址、改地址的 context。
	f, _ = a.GetContact(acc, id)
	for i := range f.Emails {
		if f.Emails[i].Key == "mainMail" {
			f.Emails[i].Label = "work mail"
		}
	}
	f.Emails = append(f.Emails, ContactField{Value: "czlmail-test-new@example.com"})
	for i := range f.OnlineServices {
		if f.OnlineServices[i].Link {
			f.OnlineServices[i].URI = "https://example.com/jana/about"
		}
	}
	f.Addresses[0].Context = "private"
	if _, err := a.SaveContact(*f); err != nil {
		t.Fatalf("save edited: %v", err)
	}
	edited := server()
	t.Logf("after edit: %s", mustJSON(edited))

	var emails map[string]map[string]any
	_ = json.Unmarshal(edited["emails"], &emails)
	main := emails["mainMail"]
	if len(emails) != 3 || main == nil || main["label"] != "work mail" || main["pref"] != float64(1) ||
		main["contexts"].(map[string]any)["private"] != true {
		t.Errorf("emails: %s", edited["emails"])
	}
	if string(before["phones"]) != string(edited["phones"]) || string(before["organizations"]) != string(edited["organizations"]) {
		t.Errorf("untouched properties changed: phones %s organizations %s", edited["phones"], edited["organizations"])
	}
	var links map[string]map[string]any
	_ = json.Unmarshal(edited["links"], &links)
	if s := links["site"]; len(links) != 1 || s == nil || s["uri"] != "https://example.com/jana/about" || s["kind"] != "contact" || s["mediaType"] != "text/html" {
		t.Errorf("links: %s", edited["links"])
	}
	var addrs map[string]map[string]any
	_ = json.Unmarshal(edited["addresses"], &addrs)
	if o := addrs["office"]; o == nil || o["countryCode"] != "CZ" || len(o["components"].([]any)) != 3 || o["contexts"].(map[string]any)["private"] != true {
		t.Errorf("addresses: %s", edited["addresses"])
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
