package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Probe tests the IMAP and SMTP servers of cfg: sign in, capabilities, and for
// SMTP EHLO + AUTH without sending anything.
func Probe(ctx context.Context, cfg upstream.Config) (*upstream.ProbeReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	rep := &upstream.ProbeReport{}
	c, err := connect(ctx, cfg, Options{SignIn: defaultSignIn}, nil)
	if err != nil {
		rep.IMAP = upstream.ProbeFailure(err)
	} else {
		rep.IMAP = upstream.ProbePart{OK: true, Capabilities: capabilities(ctx, c)}
		c.Logout(ctx)
	}
	if cfg.SMTP.Host == "" {
		return rep, nil
	}
	sc, mechs, err := DialSMTP(ctx, cfg)
	if err != nil {
		f := upstream.ProbeFailure(err)
		f.Auth = mechs
		rep.SMTP = &f
		return rep, nil
	}
	sc.Quit()
	rep.SMTP = &upstream.ProbePart{OK: true, Auth: mechs}
	return rep, nil
}

// DialSMTP connects to cfg's SMTP server through its dialer, says EHLO, upgrades
// to TLS as configured and authenticates. It returns the AUTH mechanisms offered.
// #41 sends through the returned client.
func DialSMTP(ctx context.Context, cfg upstream.Config) (*smtp.Client, []string, error) {
	ep := cfg.SMTP
	addr := net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port))
	conn, err := cfg.Dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, upstream.Classify("smtp connect", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	tc := &tls.Config{ServerName: ep.Host}
	if ep.TLS == imapc.TLSImplicit {
		t := tls.Client(conn, tc)
		if err := t.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, nil, upstream.Classify("smtp tls", err)
		}
		conn = t
	}
	c, err := smtp.NewClient(conn, ep.Host)
	if err != nil {
		conn.Close()
		return nil, nil, upstream.Classify("smtp greeting", err)
	}
	if err := c.Hello("czlmail-gateway"); err != nil {
		c.Close()
		return nil, nil, upstream.Classify("smtp ehlo", err)
	}
	if ep.TLS == imapc.TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return nil, nil, upstream.Classify("smtp starttls", errors.New("server does not offer STARTTLS"))
		}
		if err := c.StartTLS(tc); err != nil {
			c.Close()
			return nil, nil, upstream.Classify("smtp starttls", err)
		}
	}
	_, params := c.Extension("AUTH")
	mechs := strings.Fields(strings.ToUpper(params))
	user, pass := ep.Username, ep.Password
	if user == "" {
		user, pass = cfg.IMAP.Username, cfg.IMAP.Password
	}
	var auth smtp.Auth
	switch {
	case cfg.AccessToken != nil:
		tok, err := cfg.AccessToken(ctx)
		if err != nil {
			c.Close()
			return nil, mechs, err
		}
		auth = &xoauth2{user: user, token: tok}
	case has(mechs, "PLAIN"):
		auth = &plainAuth{user: user, pass: pass}
	case has(mechs, "LOGIN"):
		auth = &loginAuth{user: user, pass: pass}
	case len(mechs) == 0:
		return c, mechs, nil // no AUTH offered: relay without authentication
	default:
		c.Close()
		return nil, mechs, upstream.Classify("smtp auth", errors.New("no supported AUTH mechanism"))
	}
	if err := c.Auth(auth); err != nil {
		c.Close()
		return nil, mechs, upstream.Classify("smtp auth", err)
	}
	conn.SetDeadline(time.Time{})
	return c, mechs, nil
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// plainAuth is AUTH PLAIN without net/smtp's refusal of plaintext connections:
// the operator chose the TLS mode explicitly (plain only for trusted networks).
type plainAuth struct{ user, pass string }

func (a *plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a *plainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }

type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(string(fromServer)), "user") {
		return []byte(a.user), nil
	}
	return []byte(a.pass), nil
}

type xoauth2 struct{ user, token string }

func (a *xoauth2) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}
func (a *xoauth2) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil // error challenge: reply empty to get the final status
	}
	return nil, nil
}
