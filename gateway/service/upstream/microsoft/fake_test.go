package microsoft_test

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// exchangeIMAP is a scripted stand-in for outlook.office365.com: Exchange Online's
// greeting, capabilities and answers for what a connection test sends. It records
// every XOAUTH2 initial response.
type exchangeIMAP struct {
	ln net.Listener
	// token is the only access token accepted; anything else gets Exchange's
	// "NO AUTHENTICATE failed."
	token string
	// notConnected makes every command after a good AUTHENTICATE fail the way
	// Exchange does when IMAP is off for the mailbox.
	notConnected bool

	mu    sync.Mutex
	sasl  []string
	lines []string
}

const exchangeCaps = "IMAP4 IMAP4rev1 AUTH=PLAIN AUTH=XOAUTH2 SASL-IR UIDPLUS MOVE ID UNSELECT CHILDREN IDLE NAMESPACE LITERAL+"

func newExchangeIMAP(t *testing.T, token string) *exchangeIMAP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &exchangeIMAP{ln: ln, token: token}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *exchangeIMAP) Addr() string { return s.ln.Addr().String() }

func (s *exchangeIMAP) SASL() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sasl...)
}

func (s *exchangeIMAP) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func (s *exchangeIMAP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(format string, a ...any) { fmt.Fprintf(c, format+"\r\n", a...) }
	w("* OK The Microsoft Exchange IMAP4 service is ready. [TQBXADIAUABSADEAMAAwAEMAQQAwADAAMQAuAG4AYQBtAHAAcgBkADEAMAAuAHAAcgBvAGQALgBvAHUAdABsAG8AbwBrAC4AYwBvAG0A]")
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.lines = append(s.lines, line)
		s.mu.Unlock()
		tag, rest, _ := strings.Cut(line, " ")
		cmd, args, _ := strings.Cut(rest, " ")
		cmd = strings.ToUpper(cmd)
		if authed && s.notConnected && cmd != "LOGOUT" {
			w("%s BAD User is authenticated but not connected.", tag)
			continue
		}
		switch cmd {
		case "CAPABILITY":
			w("* CAPABILITY %s", exchangeCaps)
			w("%s OK CAPABILITY completed.", tag)
		case "AUTHENTICATE":
			mech, ir, _ := strings.Cut(args, " ")
			if !strings.EqualFold(mech, "XOAUTH2") || ir == "" {
				w("%s BAD Command Argument Error. 12", tag)
				continue
			}
			raw, _ := base64.StdEncoding.DecodeString(ir)
			s.mu.Lock()
			s.sasl = append(s.sasl, string(raw))
			s.mu.Unlock()
			if !strings.HasSuffix(string(raw), "auth=Bearer "+s.token+"\x01\x01") {
				w("%s NO AUTHENTICATE failed.", tag)
				continue
			}
			authed = true
			w("%s OK AUTHENTICATE completed.", tag)
		case "ID":
			w(`* ID ("name" "Microsoft.Exchange.Imap4.Imap4Server" "version" "15.20")`)
			w("%s OK ID completed.", tag)
		case "ENABLE":
			w("%s BAD Command Error. 10", tag) // Exchange has no ENABLE
		case "SELECT", "EXAMINE":
			w("* 0 EXISTS")
			w("* 0 RECENT")
			w(`* FLAGS (\Seen \Answered \Flagged \Deleted \Draft $MDNSent)`)
			w(`* OK [PERMANENTFLAGS ()] Permanent flags`)
			w("* OK [UIDVALIDITY 14] UIDVALIDITY value")
			w("* OK [UIDNEXT 1] The next unique identifier value")
			w("%s OK [READ-ONLY] %s completed.", tag, cmd)
		case "UNSELECT":
			w("%s OK UNSELECT completed.", tag)
		case "NOOP":
			w("%s OK NOOP completed.", tag)
		case "LOGOUT":
			w("* BYE Microsoft Exchange Server IMAP4 server signing off.")
			w("%s OK LOGOUT completed.", tag)
			return
		default: // UID FETCH ... X-GUID and anything else
			w("%s BAD Command Argument Error. 11", tag)
		}
	}
}

// exchangeSMTP is a scripted stand-in for smtp.office365.com (after STARTTLS).
type exchangeSMTP struct {
	ln    net.Listener
	token string
	// authDisabled answers like a tenant with SMTP AUTH turned off.
	authDisabled bool

	mu   sync.Mutex
	sasl []string
}

func newExchangeSMTP(t *testing.T, token string) *exchangeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &exchangeSMTP{ln: ln, token: token}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *exchangeSMTP) Addr() string { return s.ln.Addr().String() }

func (s *exchangeSMTP) SASL() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sasl...)
}

func (s *exchangeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(format string, a ...any) { fmt.Fprintf(c, format+"\r\n", a...) }
	w("220 SN4PR0201CA0012.outlook.office365.com Microsoft ESMTP MAIL Service ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, args, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			w("250-SN4PR0201CA0012.outlook.office365.com Hello [192.0.2.1]")
			w("250-SIZE 157286400")
			w("250-8BITMIME")
			w("250-AUTH LOGIN XOAUTH2")
			w("250 SMTPUTF8")
		case "AUTH":
			mech, ir, _ := strings.Cut(args, " ")
			if !strings.EqualFold(mech, "XOAUTH2") {
				w("504 5.7.4 Unrecognized authentication type")
				continue
			}
			if ir == "" { // two-step form
				w("334 ")
				ir, _ = r.ReadString('\n')
				ir = strings.TrimRight(ir, "\r\n")
			}
			raw, _ := base64.StdEncoding.DecodeString(ir)
			s.mu.Lock()
			s.sasl = append(s.sasl, string(raw))
			s.mu.Unlock()
			switch {
			case s.authDisabled:
				w("535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Tenant. Visit https://aka.ms/smtp_auth_disabled for more information.")
			case !strings.HasSuffix(string(raw), "auth=Bearer "+s.token+"\x01\x01"):
				w("535 5.7.3 Authentication unsuccessful [SN4PR0201CA0012.namprd02.prod.outlook.com]")
			default:
				w("235 2.7.0 Authentication successful")
			}
		case "QUIT":
			w("221 2.0.0 Service closing transmission channel")
			return
		default:
			w("500 5.3.3 Unrecognized command")
		}
	}
}
