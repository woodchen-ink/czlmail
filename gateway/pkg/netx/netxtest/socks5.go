// Package netxtest is a minimal SOCKS5 server for tests (RFC 1928 CONNECT with
// no-auth or username/password, RFC 1929). Test code only.
package netxtest

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// SOCKS5 is a running proxy on a loopback port.
type SOCKS5 struct {
	ln       net.Listener
	user     string
	password string

	// Connects counts successful CONNECTs; Targets records the requested addresses.
	Connects atomic.Int64
	mu       sync.Mutex
	targets  []string
	conns    map[net.Conn]struct{}
	stopped  bool
}

// Start runs a proxy; user "" accepts clients without authentication.
// It stops when the test ends.
func Start(t testing.TB, user, password string) *SOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SOCKS5{ln: ln, user: user, password: password, conns: map[net.Conn]struct{}{}}
	go s.accept()
	t.Cleanup(s.Stop)
	return s
}

// Addr is host:port of the proxy.
func (s *SOCKS5) Addr() string { return s.ln.Addr().String() }

// Host and Port split Addr.
func (s *SOCKS5) Host() string { h, _, _ := net.SplitHostPort(s.Addr()); return h }

// Port is the proxy's port.
func (s *SOCKS5) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr())
	n, _ := strconv.Atoi(p)
	return n
}

// Targets returns the addresses clients asked to connect to, as sent (names unresolved).
func (s *SOCKS5) Targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.targets...)
}

// Stop closes the listener and every relayed connection, like a proxy going down.
func (s *SOCKS5) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	s.ln.Close()
	for c := range s.conns {
		c.Close()
	}
}

func (s *SOCKS5) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		c.Close()
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *SOCKS5) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		if !s.track(c) {
			return
		}
		go s.serve(c)
	}
}

func (s *SOCKS5) serve(c net.Conn) {
	defer c.Close()
	target, err := s.handshake(c)
	if err != nil {
		return
	}
	up, err := net.Dial("tcp", target)
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	if !s.track(up) {
		return
	}
	defer up.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	s.Connects.Add(1)
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

var errHandshake = errors.New("socks5 handshake")

func (s *SOCKS5) handshake(c net.Conn) (string, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil || hdr[0] != 5 {
		return "", errHandshake
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	want := byte(0)
	if s.user != "" {
		want = 2
	}
	if !contains(methods, want) {
		c.Write([]byte{5, 0xff})
		return "", errHandshake
	}
	c.Write([]byte{5, want})
	if want == 2 {
		if err := s.authenticate(c); err != nil {
			return "", err
		}
	}
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil || req[1] != 1 {
		return "", errHandshake
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		io.ReadFull(c, b)
		host = net.IP(b).String()
	case 3:
		var n [1]byte
		io.ReadFull(c, n[:])
		b := make([]byte, n[0])
		io.ReadFull(c, b)
		host = string(b)
	case 4:
		b := make([]byte, 16)
		io.ReadFull(c, b)
		host = net.IP(b).String()
	default:
		return "", errHandshake
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return "", err
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	s.mu.Lock()
	s.targets = append(s.targets, target)
	s.mu.Unlock()
	return target, nil
}

func (s *SOCKS5) authenticate(c net.Conn) error {
	var ver [2]byte
	if _, err := io.ReadFull(c, ver[:]); err != nil {
		return err
	}
	user := make([]byte, ver[1])
	io.ReadFull(c, user)
	var n [1]byte
	io.ReadFull(c, n[:])
	pass := make([]byte, n[0])
	io.ReadFull(c, pass)
	if string(user) != s.user || string(pass) != s.password {
		c.Write([]byte{1, 1})
		return errHandshake
	}
	_, err := c.Write([]byte{1, 0})
	return err
}

func contains(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
