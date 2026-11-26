// Package netx builds the outbound dialer of one upstream: direct with a
// private-network guard, or through a SOCKS5 proxy with remote name resolution.
// IMAP, SMTP and HTTP clients of that upstream all dial through it.
package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// Dialer opens TCP connections for one upstream.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// ErrPrivateAddress means a host resolves to a loopback, private, link-local or
// otherwise internal address while private networks are not allowed.
var ErrPrivateAddress = errors.New("netx: address is in a private network")

// ProxyError wraps a failure to reach or use the SOCKS5 proxy, so callers can tell
// "the proxy is down" from "the mail server is down".
type ProxyError struct{ Err error }

func (e *ProxyError) Error() string { return "proxy: " + e.Err.Error() }
func (e *ProxyError) Unwrap() error { return e.Err }

// Proxy is a SOCKS5 proxy. Username and Password are optional.
type Proxy struct {
	Host     string
	Port     int
	Username string
	Password string
}

// Options configure a Dialer.
type Options struct {
	// Proxy routes every connection through SOCKS5; nil dials directly.
	Proxy *Proxy
	// AllowPrivate lets hosts resolve to internal addresses
	// (setting security.allow_private_networks).
	AllowPrivate bool
	// Timeout bounds connection setup, including the SOCKS5 handshake. Default 20 s.
	Timeout time.Duration
	// Resolver overrides net.DefaultResolver; tests only.
	Resolver *net.Resolver
}

// New returns a Dialer for opts.
func New(opts Options) Dialer {
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.Resolver == nil {
		opts.Resolver = net.DefaultResolver
	}
	d := &direct{allowPrivate: opts.AllowPrivate, resolver: opts.Resolver, dialer: &net.Dialer{Timeout: opts.Timeout, KeepAlive: 30 * time.Second}}
	if opts.Proxy == nil {
		return d
	}
	return &socks{direct: d, proxy: *opts.Proxy, timeout: opts.Timeout}
}

// ParseProxyURL parses socks5://[user@]host:port. A password in the URL is
// rejected: it is stored encrypted, apart from the URL.
func ParseProxyURL(s string) (*Proxy, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "socks5" || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return nil, errors.New("proxy must look like socks5://[user@]host:port")
	}
	if _, set := u.User.Password(); set {
		return nil, errors.New("proxy password belongs in its own field")
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, errors.New("proxy must have a port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 || host == "" {
		return nil, errors.New("invalid proxy port")
	}
	return &Proxy{Host: host, Port: port, Username: u.User.Username()}, nil
}

// HTTPClient returns an HTTP client whose connections go through d.
func HTTPClient(d Dialer, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         d.DialContext,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 15 * time.Second,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConns:        4,
		},
	}
}

// IsPrivate reports whether ip is an address the guard refuses: loopback,
// RFC 1918 / ULA private, link-local (cloud metadata), CGNAT, unspecified,
// multicast, or an IPv4-mapped form of one of them.
func IsPrivate(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		cgnat.Contains(ip) || benchmarking.Contains(ip) || !ip.IsValid()
}

var (
	cgnat        = netip.MustParsePrefix("100.64.0.0/10")
	benchmarking = netip.MustParsePrefix("198.18.0.0/15")
)

type direct struct {
	allowPrivate bool
	resolver     *net.Resolver
	dialer       *net.Dialer
}

// DialContext resolves addr, checks every address, and dials the checked ones,
// so a DNS answer cannot change between the check and the connection.
func (d *direct) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ips, port, err := d.resolve(ctx, addr)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, ip := range ips {
		c, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

func (d *direct) resolve(ctx context.Context, addr string) ([]netip.Addr, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, "", err
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = d.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, "", err
		}
	}
	if len(ips) == 0 {
		return nil, "", fmt.Errorf("netx: %s has no address", host)
	}
	if !d.allowPrivate {
		for _, ip := range ips {
			if IsPrivate(ip) {
				return nil, "", fmt.Errorf("%w: %s", ErrPrivateAddress, host)
			}
		}
	}
	return ips, port, nil
}

type socks struct {
	direct  *direct
	proxy   Proxy
	timeout time.Duration
}

// DialContext connects to the proxy through the guarded direct dialer and asks it
// to connect to addr by name, so the proxy resolves it. An IP literal target is
// still checked against the guard.
func (s *socks) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip, err := netip.ParseAddr(host); err == nil && !s.direct.allowPrivate && IsPrivate(ip) {
		return nil, fmt.Errorf("%w: %s", ErrPrivateAddress, host)
	}
	var auth *proxy.Auth
	if s.proxy.Username != "" || s.proxy.Password != "" {
		auth = &proxy.Auth{User: s.proxy.Username, Password: s.proxy.Password}
	}
	pd, err := proxy.SOCKS5("tcp", net.JoinHostPort(s.proxy.Host, strconv.Itoa(s.proxy.Port)), auth, s.direct)
	if err != nil {
		return nil, &ProxyError{err}
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	c, err := pd.(proxy.ContextDialer).DialContext(ctx, network, addr)
	if err != nil {
		return nil, classifySOCKS(err)
	}
	return c, nil
}

// classifySOCKS keeps "the target refused" apart from "the proxy is unusable":
// x/net/proxy reports the proxy's reply codes as "unknown error <reason>".
func classifySOCKS(err error) error {
	if errors.Is(err, ErrPrivateAddress) {
		return err
	}
	return &ProxyError{err}
}

// Dial adapts direct to proxy.Dialer for x/net/proxy.
func (d *direct) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

// CheckHost resolves host with the system resolver and returns ErrPrivateAddress
// when any of its addresses is internal. Resolution errors are returned as is.
func CheckHost(ctx context.Context, host string) error {
	d := &direct{resolver: net.DefaultResolver}
	_, _, err := d.resolve(ctx, net.JoinHostPort(host, "0"))
	return err
}
