package imapc

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// With SASL-IR the XOAUTH2 initial response goes on the command line, and the
// error challenge Gmail and Microsoft send before failing is answered with an
// empty line, so the client gets the tagged NO instead of hanging.
func TestXOAuth2InlineAndErrorChallenge(t *testing.T) {
	cli, srv := net.Pipe()
	got := make(chan []string, 1)
	go func() {
		defer srv.Close()
		br := bufio.NewReader(srv)
		srv.Write([]byte("* OK [CAPABILITY IMAP4rev1 SASL-IR AUTH=XOAUTH2] ready\r\n"))
		var lines []string
		authTag := ""
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimRight(line, "\r\n")
			lines = append(lines, line)
			tag, rest, _ := strings.Cut(line, " ")
			switch {
			case strings.HasPrefix(rest, "CAPABILITY"):
				srv.Write([]byte("* CAPABILITY IMAP4rev1 SASL-IR AUTH=XOAUTH2\r\n" + tag + " OK done\r\n"))
			case strings.HasPrefix(rest, "AUTHENTICATE"):
				authTag = tag
				srv.Write([]byte("+ eyJzdGF0dXMiOiI0MDAifQ==\r\n"))
			case line == "" && authTag != "":
				srv.Write([]byte(authTag + " NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)\r\n"))
				got <- lines
				return
			default:
				srv.Write([]byte(tag + " OK\r\n"))
			}
		}
		got <- lines
	}()
	c, err := Dial(context.Background(), func(context.Context, string, string) (net.Conn, error) { return cli, nil },
		"example.test:143", Options{TLS: TLSNone, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.LoginXOAuth2(context.Background(), "me@gmail.test", "expired")
	var se *StatusError
	if !errors.As(err, &se) || se.Status != "NO" || se.Code != "AUTHENTICATIONFAILED" {
		t.Fatalf("LoginXOAuth2 = %v, want NO [AUTHENTICATIONFAILED]", err)
	}
	var auth string
	for _, l := range <-got {
		if strings.Contains(l, "AUTHENTICATE") {
			auth = l
		}
	}
	if !strings.Contains(auth, "AUTHENTICATE XOAUTH2 ") || len(strings.Fields(auth)) != 4 {
		t.Fatalf("AUTHENTICATE line %q: the initial response must be inline with SASL-IR", auth)
	}
}
