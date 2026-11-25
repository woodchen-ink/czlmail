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
