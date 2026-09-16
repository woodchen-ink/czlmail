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
