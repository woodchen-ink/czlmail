// Package smtptest points tests at the Mailpit SMTP sink in docker-compose.test.yml
// and reads back what was delivered through its HTTP API. Test code only.
//
//	GATEWAY_TEST_SMTP_ADDR    SMTP submission, any credentials accepted, 127.0.0.1:1025
//	GATEWAY_TEST_MAILPIT_URL  Mailpit API, http://127.0.0.1:8025
//
// Mailpit is shared by every test, so send each test's mail to its own recipient
// (UniqueAddress) and look it up by that address.
package smtptest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Environment variables naming the Mailpit endpoints.
const (
	EnvSMTPAddr   = "GATEWAY_TEST_SMTP_ADDR"
	EnvMailpitURL = "GATEWAY_TEST_MAILPIT_URL"
)

// Mailpit is the SMTP sink plus its API.
type Mailpit struct {
	SMTPAddr string
	APIURL   string
	t        testing.TB
}

// Address is a mailbox in a Mailpit message summary.
type Address struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// Message is Mailpit's message summary.
type Message struct {
	ID        string    `json:"ID"`
	MessageID string    `json:"MessageID"`
	From      Address   `json:"From"`
	To        []Address `json:"To"`
	Cc        []Address `json:"Cc"`
	Bcc       []Address `json:"Bcc"`
	Subject   string    `json:"Subject"`
	Size      int       `json:"Size"`
}

// New returns the Mailpit endpoints, or skips the test when they are not configured.
func New(t testing.TB) *Mailpit {
	t.Helper()
	smtp := strings.TrimSpace(os.Getenv(EnvSMTPAddr))
	api := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvMailpitURL)), "/")
	if smtp == "" || api == "" {
		t.Skipf("%s / %s not set; start docker-compose.test.yml to run this test", EnvSMTPAddr, EnvMailpitURL)
	}
	return &Mailpit{SMTPAddr: smtp, APIURL: api, t: t}
}

// UniqueAddress returns a recipient address no other test uses.
func UniqueAddress(local string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%s@mailpit.test", local, hex.EncodeToString(b))
}

// Search runs a Mailpit search such as "to:x@y" and returns the matches, newest first.
func (m *Mailpit) Search(query string) []Message {
	m.t.Helper()
	var out struct {
		Messages []Message `json:"messages"`
	}
	m.getJSON("/api/v1/search?query="+url.QueryEscape(query), &out)
	return out.Messages
}

// WaitFor polls Search until it returns n messages or the timeout passes.
func (m *Mailpit) WaitFor(query string, n int, timeout time.Duration) []Message {
	m.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		msgs := m.Search(query)
		if len(msgs) >= n {
			return msgs
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("smtptest: %q: %d messages after %s, want %d", query, len(msgs), timeout, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Raw returns the message source as delivered.
func (m *Mailpit) Raw(id string) []byte {
	m.t.Helper()
	return m.get("/api/v1/message/" + url.PathEscape(id) + "/raw")
}

func (m *Mailpit) getJSON(path string, v any) {
	m.t.Helper()
	if err := json.Unmarshal(m.get(path), v); err != nil {
		m.t.Fatalf("smtptest: decode %s: %v", path, err)
	}
}

func (m *Mailpit) get(path string) []byte {
	m.t.Helper()
	resp, err := http.Get(m.APIURL + path)
	if err != nil {
		m.t.Fatalf("smtptest: GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		m.t.Fatalf("smtptest: GET %s: %s %v %s", path, resp.Status, err, body)
	}
	return body
}
