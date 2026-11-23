package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// 1x1 透明 GIF。
var tinyGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// 站点不支持 https 时(这里是同一端口上的明文服务器, TLS 握手失败)退回原来的 http 地址。
func TestFetchRemoteImageFallsBackToHTTP(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/gif")
		w.Write(tinyGIF)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL + "/logo.gif")
	client := &http.Client{Timeout: 5 * time.Second}
	data, typ, err := fetchRemoteImageUpgrading(context.Background(), client, u)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if typ != "image/gif" || len(data) != len(tinyGIF) {
		t.Fatalf("got %q, %d bytes", typ, len(data))
	}
	if hits != 1 {
		t.Fatalf("http server hit %d times, want 1", hits)
	}
}

// https 地址不做任何改写, 失败就是失败。
func TestFetchRemoteImageHTTPSNoFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tinyGIF)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL + "/logo.gif")
	u.Scheme = "https"
	client := &http.Client{Timeout: 5 * time.Second}
	if _, _, err := fetchRemoteImageUpgrading(context.Background(), client, u); err == nil {
		t.Fatal("https fetch from a plain-text server should fail")
	}
}

// 站点支持 https 时, http 地址改走 https, 不发明文请求。
func TestFetchRemoteImageUpgradesToHTTPS(t *testing.T) {
	var scheme string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			scheme = "https"
		}
		w.Write(tinyGIF)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL + "/logo.gif")
	u.Scheme = "http"
	data, typ, err := fetchRemoteImageUpgrading(context.Background(), srv.Client(), u)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if typ == "" || len(data) != len(tinyGIF) || scheme != "https" {
		t.Fatalf("got %q, %d bytes over %q", typ, len(data), scheme)
	}
}
