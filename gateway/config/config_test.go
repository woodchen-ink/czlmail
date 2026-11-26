package config

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
)

var goodKey = base64.StdEncoding.EncodeToString(make([]byte, MasterKeySize))

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com/"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://gw.example.com" || cfg.Listen != ":8080" || cfg.DataDir != "/data" || cfg.Proxies != nil {
		t.Fatalf("unexpected config %+v", cfg)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing key":      {EnvBaseURL: "https://gw.example.com"},
		"short key":        {EnvMasterKey: base64.StdEncoding.EncodeToString(make([]byte, 16)), EnvBaseURL: "https://gw.example.com"},
		"bad base64":       {EnvMasterKey: "not base64!", EnvBaseURL: "https://gw.example.com"},
		"missing base":     {EnvMasterKey: goodKey},
		"http public host": {EnvMasterKey: goodKey, EnvBaseURL: "http://gw.example.com"},
		"relative base":    {EnvMasterKey: goodKey, EnvBaseURL: "gw.example.com"},
		"base with query":  {EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com/?x=1"},
		"proxies no cidr":  {EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com", EnvTrustedProxies: "X-Real-IP@"},
		"proxies bad cidr": {EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com", EnvTrustedProxies: "X-Real-IP@300.0.0.0/8"},
	}
	for name, m := range cases {
		if _, err := load(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := load(env(nil))
	if err == nil || !strings.Contains(err.Error(), EnvMasterKey) || !strings.Contains(err.Error(), EnvBaseURL) {
		t.Fatalf("err = %v, want both variables named", err)
	}
}

func TestLocalhostHTTPAllowed(t *testing.T) {
	if _, err := load(env(map[string]string{EnvMasterKey: goodKey, EnvBaseURL: "http://localhost:8080"})); err != nil {
		t.Fatal(err)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.2:5555"
	r.Header.Set("X-Real-IP", "203.0.113.9")

	direct, _ := load(env(map[string]string{EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com"}))
	if got := direct.ClientIP(r); got != "172.18.0.2" {
		t.Errorf("without trusted proxies: %q", got)
	}
	proxied, err := load(env(map[string]string{EnvMasterKey: goodKey, EnvBaseURL: "https://gw.example.com", EnvTrustedProxies: "X-Real-IP@172.16.0.0/12"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := proxied.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("behind trusted proxy: %q", got)
	}
}
