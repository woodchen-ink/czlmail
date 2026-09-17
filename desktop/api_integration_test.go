package main

import "testing"

func TestParseMailto(t *testing.T) {
	r := parseMailto("mailto:a@example.com,b@example.com?cc=c@example.com&subject=%E4%BD%A0%E5%A5%BD&body=line1%0Aline2")
	if r.To != "a@example.com,b@example.com" || r.CC != "c@example.com" || r.Subject != "你好" || r.Body != "line1\nline2" {
		t.Fatalf("%+v", r)
	}
	r = parseMailto("mailto:?to=x@example.com")
	if r.To != "x@example.com" {
		t.Fatalf("%+v", r)
	}
}

func TestParseMailLink(t *testing.T) {
	r, ok := parseMailLink("czlmail://email/c/Mabc123")
	if !ok || r.Kind != "email" || r.AccountID != "c" || r.ID != "Mabc123" {
		t.Fatalf("%+v %v", r, ok)
	}
	r, ok = parseMailLink("czlmail://thread/a%2Fb/T1/")
	if !ok || r.Kind != "thread" || r.AccountID != "a/b" || r.ID != "T1" {
		t.Fatalf("%+v %v", r, ok)
	}
	for _, bad := range []string{"czlmail://email/c", "czlmail://file/c/x", "czlmail://email//x", "czlmail://email/c/x/y"} {
		if _, ok := parseMailLink(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}
