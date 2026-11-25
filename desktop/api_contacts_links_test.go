package main

import (
	"encoding/json"
	"testing"
)

// TestContactLinksRoundTrip: websites stay JSContact links instead of becoming an online service named "网站",
// and entries an older version saved that way are read back as websites.
func TestContactLinksRoundTrip(t *testing.T) {
	raw := []byte(`{"id":"c1","uid":"u1","kind":"individual","name":{"full":"Jana"},
		"links":{"k1":{"uri":"https://example.cz"}},
		"onlineServices":{"s1":{"service":"网站","uri":"https://old.example.cz"},"s2":{"service":"Mastodon","uri":"@jana@example.social"}}}`)
	f := contactFormFrom(raw)
	links, services := 0, 0
	for _, s := range f.OnlineServices {
		if s.Service == legacyLinkService {
			t.Fatalf("Chinese service name leaked: %+v", s)
		}
		if s.Link {
			links++
		} else if s.Service == "Mastodon" {
			services++
		}
	}
	if links != 2 || services != 1 {
		t.Fatalf("read: %+v", f.OnlineServices)
	}

	f.Given = "Jana"
	f.OnlineServices = append(f.OnlineServices, ContactService{Service: "Blog", URI: "https://blog.example.cz", Link: true})
	props, err := contactProps(f)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"links": props["links"], "onlineServices": props["onlineServices"]})
	var back struct {
		Links          map[string]struct{ URI string } `json:"links"`
		OnlineServices map[string]struct {
			Service string
			URI     string
		} `json:"onlineServices"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Links) != 2 || len(back.OnlineServices) != 2 {
		t.Fatalf("write: %s", out)
	}
	for _, s := range back.OnlineServices {
		if s.Service == "" || s.Service == legacyLinkService {
			t.Fatalf("online service without a proper name: %s", out)
		}
	}
}
