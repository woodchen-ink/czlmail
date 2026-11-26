// Package config reads the settings the gateway needs before it can open its database.
//
// Only startup settings live in environment variables: the master key, the public base URL,
// the listen address, the data directory, the trusted proxies and TZ. Everything else
// (OAuth apps, proxies, intervals, limits) is stored in the database and edited in the admin.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/woodchen-ink/go-web-utils/iputil"
)

// Environment variable names.
const (
	EnvMasterKey      = "GATEWAY_MASTER_KEY"
	EnvBaseURL        = "GATEWAY_BASE_URL"
	EnvListen         = "GATEWAY_LISTEN"
	EnvDataDir        = "GATEWAY_DATA_DIR"
	EnvTrustedProxies = "GATEWAY_TRUSTED_PROXIES"
	EnvWebDir         = "GATEWAY_WEB_DIR"
)

// MasterKeySize is the length of the decoded master key in bytes.
const MasterKeySize = 32

// Config holds the startup settings.
type Config struct {
	// MasterKey encrypts the secrets stored in the database. Never logged.
	MasterKey []byte
	// BaseURL is the public origin of the gateway without a trailing slash,
	// used for Session URLs and OAuth redirect URIs.
	BaseURL string
	// Listen is the HTTP listen address.
	Listen string
	// DataDir holds gateway.sqlite, the per-upstream databases and temporary blobs.
	DataDir string
	// WebDir is the admin web build (gateway/web/out), served under /admin/.
	WebDir string
	// Proxies decides which forwarding headers are trusted for the client IP;
	// nil when no proxy is trusted. Use ClientIP rather than reading it directly.
	Proxies *iputil.TrustedProxies

	trustedProxies string // as configured, for PublicEnv
}

// EnvVar is a startup variable as the admin's system page shows it.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"` // Value is only "set"
}

// Load reads and validates the environment.
func Load() (*Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (*Config, error) {
	var errs []error

	key, err := parseMasterKey(getenv(EnvMasterKey))
	if err != nil {
		errs = append(errs, err)
	}
	base, err := parseBaseURL(getenv(EnvBaseURL))
	if err != nil {
		errs = append(errs, err)
	}
	proxies, err := parseTrustedProxies(getenv(EnvTrustedProxies))
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &Config{
		MasterKey: key,
		BaseURL:   base,
		Listen:    orDefault(getenv(EnvListen), ":8080"),
		DataDir:   orDefault(getenv(EnvDataDir), "/data"),
		WebDir:    orDefault(getenv(EnvWebDir), "web/out"),
		Proxies:   proxies,

		trustedProxies: strings.TrimSpace(getenv(EnvTrustedProxies)),
	}, nil
}

func parseMasterKey(raw string) ([]byte, error) { return ParseMasterKey(EnvMasterKey, raw) }

// ParseMasterKey decodes a base64 master key read from the variable name.
func ParseMasterKey(name, raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is required: 32 random bytes in base64 (openssl rand -base64 32)", name)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if key, err = base64.RawURLEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("%s is not valid base64", name)
		}
	}
	if len(key) != MasterKeySize {
		return nil, fmt.Errorf("%s must decode to %d bytes, got %d", name, MasterKeySize, len(key))
	}
	return key, nil
}

func parseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%s is required, e.g. https://mail-gw.example.com", EnvBaseURL)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not an absolute URL", EnvBaseURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLocalHost(u.Hostname())) {
		// OAuth providers only accept https redirect URIs, plus http://localhost.
		return "", fmt.Errorf("%s must use https (http is allowed only for localhost)", EnvBaseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must not have a query or fragment", EnvBaseURL)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ClientIP is the client address for security decisions (login limits, audit).
// Without trusted proxies it is the direct peer; forwarding headers are ignored.
func (c *Config) ClientIP(r *http.Request) string {
	if c.Proxies != nil {
		return c.Proxies.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parseTrustedProxies reads "Header@cidr,cidr". An empty value trusts no proxy
// (nil), so the client IP is the direct peer.
func parseTrustedProxies(raw string) (*iputil.TrustedProxies, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	header, list, ok := strings.Cut(raw, "@")
	if !ok || strings.TrimSpace(header) == "" || strings.TrimSpace(list) == "" {
		return nil, fmt.Errorf("%s must look like X-Real-IP@172.16.0.0/12,10.0.0.0/8", EnvTrustedProxies)
	}
	var cidrs []string
	for _, c := range strings.Split(list, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cidrs = append(cidrs, c)
		}
	}
	tp, err := iputil.NewTrustedProxies(strings.TrimSpace(header), cidrs...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvTrustedProxies, err)
	}
	return tp, nil
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// PublicEnv lists the startup variables for the admin's system page, with the master
// key reduced to "set".
func (c *Config) PublicEnv() []EnvVar {
	return []EnvVar{
		{Name: EnvMasterKey, Value: "set", Secret: true},
		{Name: EnvBaseURL, Value: c.BaseURL},
		{Name: EnvListen, Value: c.Listen},
		{Name: EnvDataDir, Value: c.DataDir},
		{Name: EnvWebDir, Value: c.WebDir},
		{Name: EnvTrustedProxies, Value: c.trustedProxies},
	}
}

// SecureCookies reports whether admin cookies get the Secure flag: always, unless the
// gateway is served over http://localhost for development.
func (c *Config) SecureCookies() bool { return strings.HasPrefix(c.BaseURL, "https://") }
