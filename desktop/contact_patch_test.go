package main

import (
	"encoding/json"
	"testing"
)

// contactRawWithExtras 带编辑器不管的属性: pref、多个 context / feature、links 的 kind 与 mediaType、
// 地址的 countryCode 与细分分量、第二个组织。
const contactRawWithExtras = `{"id":"c1","uid":"u1","kind":"individual",
	"name":{"components":[{"kind":"given","value":"Jana"},{"kind":"surname","value":"Nováková"}],"isOrdered":true},
	"organizations":{"o1":{"name":"ACME"},"o2":{"name":"Second Org"}},
	"emails":{"e0":{"address":"jana@example.cz","contexts":{"work":true,"x-cell":true},"pref":1,"label":"main"},
		"k2":{"address":"home@example.cz","contexts":{"private":true}}},
	"phones":{"p9":{"number":"+420 1","features":{"mobile":true,"text":true},"pref":2}},
	"addresses":{"a1":{"components":[{"kind":"name","value":"Main"},{"kind":"number","value":"1"},{"kind":"locality","value":"Praha"}],
		"countryCode":"CZ","isOrdered":true,"contexts":{"work":true}}},
	"links":{"w1":{"uri":"https://example.cz","kind":"contact","mediaType":"text/html"}},
	"anniversaries":{"b1":{"kind":"birth","date":{"@type":"PartialDate","month":3,"day":21,"calendarScale":"gregorian"}}}}`

func patchFor(t *testing.T, edit func(f *ContactForm)) map[string]any {
	t.Helper()
	o := newContactOrig([]byte(contactRawWithExtras))
	if o == nil {
		t.Fatal("original not parsed")
	}
	f := contactFormFrom([]byte(contactRawWithExtras))
	edit(f)
	props, err := contactProps(f, o)
	if err != nil {
		t.Fatal(err)
	}
	dropUnchanged(props, f, o)
	return props
}

func entriesOf(t *testing.T, v any) map[string]map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	var m map[string]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

// TestContactPatchUnchanged: saving without edits sends nothing but the timestamp.
func TestContactPatchUnchanged(t *testing.T) {
	props := patchFor(t, func(*ContactForm) {})
	if len(props) != 1 || props["updated"] == nil {
		t.Fatalf("unchanged contact sends %v", props)
	}
}

// TestContactPatchKeepsKeysAndExtras: editing one entry keeps its key and the properties the editor ignores,
// leaves the other entries as they were, and gives new entries keys that do not collide.
func TestContactPatchKeepsKeysAndExtras(t *testing.T) {
	props := patchFor(t, func(f *ContactForm) {
		f.Emails[0].Label = "work mail"
		f.Emails = append(f.Emails, ContactField{Value: "new@example.cz"})
		f.OnlineServices[0].URI = "https://example.cz/jana"
		f.Addresses[0].Context = "private"
	})
	for _, k := range []string{"name", "organizations", "phones", "anniversaries"} {
		if _, ok := props[k]; ok {
			t.Errorf("untouched %s was sent: %v", k, props[k])
		}
	}

	emails := entriesOf(t, props["emails"])
	if len(emails) != 3 {
		t.Fatalf("emails: %v", emails)
	}
	e0 := emails["e0"]
	if e0["label"] != "work mail" || e0["pref"] != float64(1) || e0["contexts"].(map[string]any)["x-cell"] != true {
		t.Errorf("edited email lost its extras: %v", e0)
	}
	if emails["k2"]["address"] != "home@example.cz" {
		t.Errorf("other email changed: %v", emails["k2"])
	}
	for k, e := range emails {
		if e["address"] == "new@example.cz" && (k == "e0" || k == "k2") {
			t.Errorf("new email overwrote %s", k)
		}
	}

	links := entriesOf(t, props["links"])
	if w := links["w1"]; w["uri"] != "https://example.cz/jana" || w["kind"] != "contact" || w["mediaType"] != "text/html" || len(links) != 1 {
		t.Errorf("link: %v", links)
	}

	addr := entriesOf(t, props["addresses"])["a1"]
	if addr["countryCode"] != "CZ" || len(addr["components"].([]any)) != 3 || addr["contexts"].(map[string]any)["private"] != true {
		t.Errorf("address: %v", addr)
	}
}

// TestContactPatchRewritesEditedFields: a changed field is written the editor's way, and removed entries go away.
func TestContactPatchRewritesEditedFields(t *testing.T) {
	props := patchFor(t, func(f *ContactForm) {
		f.Addresses[0].Street = "Nová 5"
		f.Phones = nil
		f.Anniversaries[0].Date = "1990-03-21"
	})
	if props["phones"] != nil {
		t.Errorf("phones should be cleared: %v", props["phones"])
	}
	addr := entriesOf(t, props["addresses"])["a1"]
	comps := addr["components"].([]any)
	if len(comps) != 2 || comps[0].(map[string]any)["value"] != "Nová 5" || addr["countryCode"] != "CZ" {
		t.Errorf("address: %v", addr)
	}
	date := entriesOf(t, props["anniversaries"])["b1"]["date"].(map[string]any)
	if date["year"] != float64(1990) {
		t.Errorf("date: %v", date)
	}
}
