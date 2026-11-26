package jmapserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Push URLs come from clients, so the gateway must not become a way to reach
// the network it runs in: a push URL must be HTTPS and, unless private networks
// are allowed, every address it connects to must be public. The check runs on
// the resolved address at connect time, so DNS rebinding cannot get around it.

const maxPushURLLength = 2048

var errPrivateAddress = errors.New("push URL resolves to a non-public address")

// nonPublic are special-purpose ranges besides those netip classifies itself.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can embed any IPv4 address
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds an IPv4 address
	netip.MustParsePrefix("2001::/32"), // Teredo embeds an IPv4 address
}

// publicAddr reports whether ip is a globally routable unicast address.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// checkPushURL validates a client-supplied push URL before it is stored.
func checkPushURL(raw string, allowPrivate bool) error {
	if raw == "" || len(raw) > maxPushURLLength {
		return errors.New("url must be 1 to 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return errors.New("url must be an absolute https URL")
	}
	if u.User != nil {
		return errors.New("url must not contain credentials")
	}
	if u.Fragment != "" {
		return errors.New("url must not contain a fragment")
	}
	if allowPrivate {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errPrivateAddress
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicAddr(ip) {
		return errPrivateAddress
	}
	return nil
}

// newPushClient returns the HTTP client used for push delivery. base is cloned
// (tests pass a transport trusting their TLS server); nil uses the default.
func newPushClient(base *http.Transport, allowPrivate func() bool) *http.Client {
	var t *http.Transport
	if base != nil {
		t = base.Clone()
	} else {
		t = http.DefaultTransport.(*http.Transport).Clone()
	}
	t.Proxy = nil // a proxy would connect on our behalf and bypass the address check
	// Every connection must go through the checked DialContext below.
	t.Dial, t.DialTLS, t.DialTLSContext = nil, nil, nil //nolint:staticcheck // clearing deprecated hooks on purpose
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate != nil && allowPrivate() {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("push dial %s: %w", address, err)
			}
			if !publicAddr(ap.Addr()) {
				return errPrivateAddress
			}
			return nil
		},
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	t.MaxIdleConnsPerHost = 4
	return &http.Client{
		Transport: t,
		Timeout:   20 * time.Second,
		// A redirect would lead to a URL nobody validated.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
