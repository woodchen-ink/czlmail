package imaptest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

// Environment variables naming the test servers.
const (
	EnvIMAPAddr      = "GATEWAY_TEST_IMAP_ADDR"
	EnvIMAPBasicAddr = "GATEWAY_TEST_IMAP_BASIC_ADDR"
)

// Fixed users from testdata/dovecot/users. Any other name signs in with DefaultPassword.
const (
	Alice           = "alice"
	AlicePassword   = "alice-pass"
	Bob             = "bob"
	BobPassword     = "bob-pass"
	DefaultPassword = "test"
)

// Server is one Dovecot instance.
type Server struct {
	Addr string
	// Basic is true for the fallback tier, which does not advertise CONDSTORE,
	// QRESYNC or NOTIFY. Dovecot still honours CONDSTORE when a client asks for it
	// anyway, so gateways must decide by the advertised capabilities.
	Basic bool
}

// Dovecot returns the full-tier server, or skips the test when EnvIMAPAddr is unset.
func Dovecot(t testing.TB) *Server {
	return fromEnv(t, EnvIMAPAddr, false)
}

// DovecotBasic returns the fallback-tier server, or skips the test when EnvIMAPBasicAddr is unset.
func DovecotBasic(t testing.TB) *Server {
	return fromEnv(t, EnvIMAPBasicAddr, true)
}

func fromEnv(t testing.TB, name string, basic bool) *Server {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv(name))
	if addr == "" {
		t.Skipf("%s not set; start docker-compose.test.yml to run this test", name)
	}
	return &Server{Addr: addr, Basic: basic}
}

// User is a mailbox on a Server.
type User struct {
	Server   *Server
	Name     string
	Password string
}

// NewUser returns a user nobody else uses, with an empty mailbox
// (INBOX, Drafts, Sent, Trash, Junk, Archive).
func (s *Server) NewUser(t testing.TB) *User {
	t.Helper()
	name := "t" + time.Now().UTC().Format("0102150405") + randHex(6)
	return &User{Server: s, Name: name, Password: DefaultPassword}
}

// User returns a user with known credentials, such as Alice.
func (s *Server) User(name, password string) *User {
	return &User{Server: s, Name: name, Password: password}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
