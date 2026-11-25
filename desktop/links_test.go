package main

import "testing"

func TestExternalURL(t *testing.T) {
	ok := []string{
		"https://example.com/",
		"http://example.com/a?b=1&c=2",
		// Wails 的 BrowserOpenURL 会丢弃下面这些, 但它们都是合法地址。
		"https://example.com/~user/",
		"https://example.com/#!/page",
		"https://example.com/a(1)?q=a;b&x=*&y=$",
		"https://example.com/p?u=[1]&v={x}|y",
		"HTTPS://Example.com/",
		"mailto:someone@example.com?subject=Hi",
	}
	for _, raw := range ok {
		if _, good := externalURL(raw); !good {
			t.Errorf("externalURL(%q) rejected, want accepted", raw)
		}
	}

	bad := []string{
		"",
		"file:///C:/Windows/System32/calc.exe",
		"javascript:alert(1)",
		"data:text/html,x",
		"ms-settings:",
		"C:\\Windows\\System32\\calc.exe",
		"\\\\server\\share\\x.exe",
		"https:///nohost",
		"https://example.com/a b",
		"https://example.com/\x00",
		"https://example.com/\n",
		"mailto:",
		"//example.com/",
	}
	for _, raw := range bad {
		if u, good := externalURL(raw); good {
			t.Errorf("externalURL(%q) = %q, want rejected", raw, u)
		}
	}
}

func TestExternalURLMailtoParams(t *testing.T) {
	got, ok := externalURL("mailto:a@b.com?Subject=Hi&attach=C:/secret.txt&BODY=x&cc=c@d.com&ATTACH=y")
	if !ok {
		t.Fatal("mailto rejected")
	}
	want := "mailto:a@b.com?Subject=Hi&BODY=x&cc=c@d.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, ok = externalURL("mailto:a@b.com?attach=x")
	if !ok || got != "mailto:a@b.com" {
		t.Errorf("got %q %v, want bare mailto", got, ok)
	}
}

func TestMeetingURL(t *testing.T) {
	for _, raw := range []string{
		"https://teams.microsoft.com/l/meetup-join/x",
		"msteams://teams.microsoft.com/l/meetup-join/x",
		"MSTEAMS:/l/meetup-join/x",
		"zoommtg://zoom.us/join?confno=123",
		"webex://meet/abc",
	} {
		if _, ok := meetingURL(raw); !ok {
			t.Errorf("meetingURL(%q) rejected, want accepted", raw)
		}
	}
	for _, raw := range []string{
		"", "file:///C:/x.exe", "javascript:alert(1)", "data:text/html,x", "ms-settings:",
		`\\server\share`, "msteams:", "zoommtg://", "msteams://a b",
	} {
		if u, ok := meetingURL(raw); ok {
			t.Errorf("meetingURL(%q) = %q, want rejected", raw, u)
		}
	}
	if _, ok := externalURL("msteams://x/y"); ok {
		t.Error("externalURL must not accept meeting schemes")
	}
}
