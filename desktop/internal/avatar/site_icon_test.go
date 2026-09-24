package avatar

import (
	"reflect"
	"testing"
)

func TestIconLinks(t *testing.T) {
	page := `<html><head>
<link rel="stylesheet" href="/a.css">
<link rel="shortcut icon" href="/favicon.ico">
<link rel="icon" sizes="192x192" href="/big.png">
<link rel="apple-touch-icon" href="https://cdn.example.com/touch.png">
</head><body><link rel="icon" href="/ignored.png"></body></html>`
	got := iconLinks([]byte(page))
	want := []string{"https://cdn.example.com/touch.png", "/big.png", "/favicon.ico"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("iconLinks = %v, want %v", got, want)
	}
}

func TestImageType(t *testing.T) {
	ico := append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 80)...)
	if got := imageType(ico, "text/plain"); got != "image/x-icon" {
		t.Errorf("ico sniff = %q", got)
	}
	if got := imageType([]byte("<!doctype html><html>not found</html>"), "image/x-icon"); got != "" {
		t.Errorf("soft 404 accepted as %q", got)
	}
	if got := imageType([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "image/svg+xml"); got != "image/svg+xml" {
		t.Errorf("svg = %q", got)
	}
}
