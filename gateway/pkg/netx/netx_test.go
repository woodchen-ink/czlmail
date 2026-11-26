package netx_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx/netxtest"
)

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				c.Write([]byte("echo " + line))
			}()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, d netx.Dialer, addr string) {
	t.Helper()
	c, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()
	c.Write([]byte("hi\n"))
	got, _ := bufio.NewReader(c).ReadString('\n')
	if got != "echo hi\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDirectGuard(t *testing.T) {
	addr := echoServer(t)
	_, err := netx.New(netx.Options{}).DialContext(context.Background(), "tcp", addr)
	if !errors.Is(err, netx.ErrPrivateAddress) {
		t.Fatalf("loopback without allow: %v", err)
	}
	_, err = netx.New(netx.Options{}).DialContext(context.Background(), "tcp", "localhost:1")
	if !errors.Is(err, netx.ErrPrivateAddress) {
		t.Fatalf("localhost name without allow: %v", err)
	}
	roundTrip(t, netx.New(netx.Options{AllowPrivate: true}), addr)
}

func TestSOCKS5(t *testing.T) {
	addr := echoServer(t)
	_, port, _ := net.SplitHostPort(addr)
	for _, auth := range []struct{ user, pass string }{{"", ""}, {"u", "p w"}} {
		px := netxtest.Start(t, auth.user, auth.pass)
		d := netx.New(netx.Options{AllowPrivate: true, Proxy: &netx.Proxy{Host: px.Host(), Port: px.Port(), Username: auth.user, Password: auth.pass}})
		// A name is passed to the proxy unresolved.
		roundTrip(t, d, "localhost:"+port)
		if got := px.Targets(); len(got) != 1 || got[0] != "localhost:"+port {
			t.Fatalf("targets %v", got)
		}
	}
}

func TestSOCKS5Failures(t *testing.T) {
	addr := echoServer(t)
	px := netxtest.Start(t, "u", "right")
	wrong := netx.New(netx.Options{AllowPrivate: true, Proxy: &netx.Proxy{Host: px.Host(), Port: px.Port(), Username: "u", Password: "wrong"}})
	var pe *netx.ProxyError
	if _, err := wrong.DialContext(context.Background(), "tcp", addr); !errors.As(err, &pe) {
		t.Fatalf("wrong proxy password: %v", err)
	}
	px.Stop()
	right := netx.New(netx.Options{AllowPrivate: true, Proxy: &netx.Proxy{Host: px.Host(), Port: px.Port(), Username: "u", Password: "right"}})
	if _, err := right.DialContext(context.Background(), "tcp", addr); !errors.As(err, &pe) {
		t.Fatalf("proxy down: %v", err)
	}
	// The proxy host itself goes through the guard.
	guarded := netx.New(netx.Options{Proxy: &netx.Proxy{Host: "127.0.0.1", Port: 1}})
	if _, err := guarded.DialContext(context.Background(), "tcp", "example.com:993"); !errors.Is(err, netx.ErrPrivateAddress) {
		t.Fatalf("private proxy host: %v", err)
	}
	// So does an IP-literal target.
	if _, err := guarded.DialContext(context.Background(), "tcp", "10.0.0.1:993"); !errors.Is(err, netx.ErrPrivateAddress) {
		t.Fatalf("private target: %v", err)
	}
}

func TestParseProxyURL(t *testing.T) {
	p, err := netx.ParseProxyURL("socks5://bob@proxy.example:1080")
	if err != nil || p.Host != "proxy.example" || p.Port != 1080 || p.Username != "bob" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range []string{"http://h:1", "socks5://h", "socks5://u:pw@h:1", "socks5://h:0", "socks5://h:1/x", "h:1"} {
		if _, err := netx.ParseProxyURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIsPrivate(t *testing.T) {
	for s, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.20.0.1": true, "192.168.1.1": true, "169.254.169.254": true,
		"100.64.0.1": true, "0.0.0.0": true, "::1": true, "fd00::1": true, "fe80::1": true, "::ffff:10.0.0.1": true,
		"8.8.8.8": false, "2606:4700::1111": false, "1.1.1.1": false,
	} {
		if got := netx.IsPrivate(netip.MustParseAddr(s)); got != want {
			t.Errorf("%s: %v", s, got)
		}
	}
}
