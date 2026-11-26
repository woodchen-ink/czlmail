package microsoft_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
)

const mailbox = "adele@contoso.test"

func endpoint(addr string) upstream.Endpoint {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return upstream.Endpoint{Host: host, Port: p, TLS: imapc.TLSNone}
}

// probeConfig points both servers at local stand-ins; an empty SMTP endpoint
// would be filled with the real smtp.office365.com.
func probeConfig(imapAddr, smtpAddr, token string) upstream.Config {
	return upstream.Config{
		AccountID:   "msft1",
		Kind:        upstream.KindMicrosoft,
		Email:       mailbox,
		IMAP:        endpoint(imapAddr),
		SMTP:        endpoint(smtpAddr),
		Dialer:      netx.New(netx.Options{AllowPrivate: true, Timeout: 5 * time.Second}),
		AccessToken: func(context.Context) (string, error) { return token, nil },
	}
}

func TestWithDefaults(t *testing.T) {
	cfg := microsoft.WithDefaults(upstream.Config{
		Email: mailbox,
		IMAP:  upstream.Endpoint{Password: "leftover"},
		SMTP:  upstream.Endpoint{Password: "leftover"},
	})
	want := upstream.Endpoint{Host: "outlook.office365.com", Port: 993, TLS: imapc.TLSImplicit, Username: mailbox}
	if cfg.IMAP != want {
		t.Errorf("IMAP %+v, want %+v", cfg.IMAP, want)
	}
	want = upstream.Endpoint{Host: "smtp.office365.com", Port: 587, TLS: imapc.TLSStartTLS, Username: mailbox}
	if cfg.SMTP != want {
		t.Errorf("SMTP %+v, want %+v", cfg.SMTP, want)
	}

	// Explicit servers and a different sign-in name (shared mailbox, alias) stay.
	cfg = microsoft.WithDefaults(upstream.Config{
		Email: mailbox,
		IMAP:  upstream.Endpoint{Host: "127.0.0.1", Port: 1143, TLS: imapc.TLSNone, Username: "adele@contoso.onmicrosoft.test"},
	})
	if cfg.IMAP.Host != "127.0.0.1" || cfg.IMAP.Port != 1143 || cfg.SMTP.Username != "adele@contoso.onmicrosoft.test" {
		t.Errorf("explicit values replaced: %+v / %+v", cfg.IMAP, cfg.SMTP)
	}
}

func TestRegistered(t *testing.T) {
	if _, err := upstream.New(upstream.Config{Kind: upstream.KindMicrosoft, Email: mailbox}, nil); err == nil {
		t.Error("factory accepted a config without an OAuth token source")
	}
	if _, err := upstream.Probe(context.Background(), upstream.Config{Kind: upstream.KindMicrosoft}); err == nil {
		t.Error("prober accepted a config without an OAuth token source")
	}
}

// The connection test signs in with XOAUTH2 on both servers, with the format
// Microsoft documents: user=<mailbox>^Aauth=Bearer <token>^A^A.
func TestProbeXOAuth2(t *testing.T) {
	const tok = "eyJ0eXAiOiJKV1QiLCJub25jZSI6Im1zZnQifQ.payload.sig"
	im := newExchangeIMAP(t, tok)
	sm := newExchangeSMTP(t, tok)
	rep, err := upstream.Probe(context.Background(), probeConfig(im.Addr(), sm.Addr(), tok))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IMAP.OK {
		t.Fatalf("IMAP failed: %+v (lines %q)", rep.IMAP, im.Lines())
	}
	if rep.SMTP == nil || !rep.SMTP.OK {
		t.Fatalf("SMTP failed: %+v", rep.SMTP)
	}
	want := "user=" + mailbox + "\x01auth=Bearer " + tok + "\x01\x01"
	if got := im.SASL(); len(got) != 1 || got[0] != want {
		t.Errorf("IMAP XOAUTH2 %q, want %q", got, want)
	}
	if got := sm.SASL(); len(got) != 1 || got[0] != want {
		t.Errorf("SMTP XOAUTH2 %q, want %q", got, want)
	}
	c := rep.IMAP.Capabilities
	if c == nil || !c.Idle || !c.Move || !c.UIDPlus || c.CondStore || c.QResync || c.Notify || c.ObjectID || c.XGUID || c.ListStatus {
		t.Errorf("capabilities %+v", c)
	}
	if len(rep.SMTP.Auth) != 2 || rep.SMTP.Auth[1] != "XOAUTH2" {
		t.Errorf("SMTP auth %v", rep.SMTP.Auth)
	}
}

func TestProbeFailures(t *testing.T) {
	const tok = "good-token"
	t.Run("rejected token", func(t *testing.T) {
		im := newExchangeIMAP(t, tok)
		sm := newExchangeSMTP(t, tok)
		rep, err := upstream.Probe(context.Background(), probeConfig(im.Addr(), sm.Addr(), "stale-token"))
		if err != nil {
			t.Fatal(err)
		}
		if rep.IMAP.OK || rep.IMAP.Code != upstream.CodeProbeAuth || rep.IMAP.Msg != "authentication_failed" {
			t.Errorf("IMAP %+v", rep.IMAP)
		}
		if rep.SMTP.OK || rep.SMTP.Code != upstream.CodeProbeAuth {
			t.Errorf("SMTP %+v", rep.SMTP)
		}
	})
	t.Run("IMAP off for the mailbox", func(t *testing.T) {
		im := newExchangeIMAP(t, tok)
		im.notConnected = true
		sm := newExchangeSMTP(t, tok)
		rep, err := upstream.Probe(context.Background(), probeConfig(im.Addr(), sm.Addr(), tok))
		if err != nil {
			t.Fatal(err)
		}
		if rep.IMAP.OK || rep.IMAP.Code != microsoft.CodeProbeIMAPDisabled || rep.IMAP.Msg != "mailbox_not_connected" {
			t.Errorf("IMAP %+v", rep.IMAP)
		}
		if rep.SMTP == nil || !rep.SMTP.OK {
			t.Errorf("SMTP %+v", rep.SMTP)
		}
	})
	t.Run("SMTP AUTH off for the tenant", func(t *testing.T) {
		im := newExchangeIMAP(t, tok)
		sm := newExchangeSMTP(t, tok)
		sm.authDisabled = true
		rep, err := upstream.Probe(context.Background(), probeConfig(im.Addr(), sm.Addr(), tok))
		if err != nil {
			t.Fatal(err)
		}
		if !rep.IMAP.OK {
			t.Errorf("IMAP %+v", rep.IMAP)
		}
		if rep.SMTP.OK || rep.SMTP.Code != microsoft.CodeProbeSMTPAuthDisabled || rep.SMTP.Msg != "smtp_auth_disabled" {
			t.Errorf("SMTP %+v", rep.SMTP)
		}
	})
	t.Run("grant gone", func(t *testing.T) {
		im := newExchangeIMAP(t, tok)
		sm := newExchangeSMTP(t, tok)
		cfg := probeConfig(im.Addr(), sm.Addr(), tok)
		cfg.AccessToken = func(context.Context) (string, error) {
			return "", &upstream.Error{Kind: upstream.KindAuth, Op: "oauth refresh", Err: microsoft.ParseTokenError(400, []byte(`{"error":"invalid_grant","error_codes":[700082]}`))}
		}
		rep, err := upstream.Probe(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if rep.IMAP.OK || rep.IMAP.Code != upstream.CodeProbeAuth {
			t.Errorf("IMAP %+v", rep.IMAP)
		}
		if rep.SMTP == nil || rep.SMTP.OK || rep.SMTP.Code != upstream.CodeProbeAuth {
			t.Errorf("SMTP %+v", rep.SMTP)
		}
		if len(im.SASL())+len(sm.SASL()) != 0 {
			t.Error("signed in without a token")
		}
	})
}

// "User is authenticated but not connected" is not a refused sign-in: a new
// authorisation does not fix it (IMAP off for the mailbox, or a mailbox not
// ready yet right after the first authorisation), so it is retried.
func TestNotConnectedIsRetried(t *testing.T) {
	for _, se := range []*imapc.StatusError{
		{Status: "BAD", Command: "CAPABILITY", Text: "User is authenticated but not connected."},
		{Status: "NO", Command: "AUTHENTICATE", Text: "User is authenticated but not connected."},
	} {
		if err := upstream.Classify("imap", se); !upstream.IsKind(err, upstream.KindUnreachable) {
			t.Errorf("%s %s: kind %v", se.Command, se.Status, err.Kind)
		}
	}
}
