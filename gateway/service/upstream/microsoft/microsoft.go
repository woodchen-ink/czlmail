// Package microsoft is the Microsoft 365 / Outlook.com upstream: the IMAP engine
// against outlook.office365.com with XOAUTH2 on both IMAP and SMTP.
//
// Exchange Online speaks plain IMAP4rev1 with IDLE, MOVE, UIDPLUS and SASL-IR, but
// no CONDSTORE, QRESYNC, NOTIFY, OBJECTID or LIST-STATUS, so the engine runs its
// basic tier: IDLE on INBOX, STATUS polling for the other folders, and the nightly
// deep compare. Folder roles come from the SPECIAL-USE attributes Exchange puts on
// LIST lines (without advertising the capability) and, failing that, from the
// names (Sent Items, Deleted Items, Junk Email, Archive and their translations).
package microsoft

import (
	"context"
	"errors"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/imap"
)

// Servers of Exchange Online, the same for work and personal accounts.
const (
	IMAPHost = "outlook.office365.com"
	IMAPPort = 993
	SMTPHost = "smtp.office365.com"
	SMTPPort = 587
)

func init() {
	upstream.Register(upstream.KindMicrosoft, New)
	upstream.RegisterProber(upstream.KindMicrosoft, Probe)
}

// errNoToken: a microsoft upstream without an OAuth token source cannot sign in.
var errNoToken = errors.New("microsoft upstream has no OAuth token")

// WithDefaults fills the Exchange Online servers and the mailbox address as the
// user name wherever cfg leaves them empty. Explicit values win, which is how the
// tests point the upstream at a local stand-in.
func WithDefaults(cfg upstream.Config) upstream.Config {
	if cfg.IMAP.Host == "" {
		cfg.IMAP.Host, cfg.IMAP.Port, cfg.IMAP.TLS = IMAPHost, IMAPPort, imapc.TLSImplicit
	}
	if cfg.SMTP.Host == "" {
		cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.TLS = SMTPHost, SMTPPort, imapc.TLSStartTLS
	}
	if cfg.IMAP.Username == "" {
		cfg.IMAP.Username = cfg.Email
	}
	if cfg.SMTP.Username == "" {
		cfg.SMTP.Username = cfg.IMAP.Username
	}
	// XOAUTH2 only: never fall back to a password.
	cfg.IMAP.Password, cfg.SMTP.Password = "", ""
	return cfg
}

// New is the upstream.Factory for microsoft.
func New(cfg upstream.Config, idx *mailindex.Store) (upstream.Upstream, error) {
	if cfg.AccessToken == nil {
		return nil, errNoToken
	}
	return imap.NewWithOptions(WithDefaults(cfg), idx, imap.Options{SignIn: signIn}), nil
}

func signIn(ctx context.Context, c *imapc.Client, cfg upstream.Config) error {
	tok, err := cfg.AccessToken(ctx)
	if err != nil {
		return err
	}
	return c.LoginXOAuth2(ctx, cfg.IMAP.Username, tok)
}

// Probe is the upstream.Prober for microsoft: the IMAP test with the Exchange
// defaults, plus readable codes for the two tenant settings that most often
// block a working token.
func Probe(ctx context.Context, cfg upstream.Config) (*upstream.ProbeReport, error) {
	if cfg.AccessToken == nil {
		return nil, errNoToken
	}
	rep, err := imap.Probe(ctx, WithDefaults(cfg))
	if err != nil {
		return nil, err
	}
	annotate(&rep.IMAP)
	if rep.SMTP != nil {
		annotate(rep.SMTP)
	}
	return rep, nil
}

// Probe codes specific to Exchange Online.
const (
	// CodeProbeSMTPAuthDisabled: the tenant (or the mailbox) has SMTP AUTH turned
	// off ("5.7.139 ... SmtpClientAuthentication is disabled").
	CodeProbeSMTPAuthDisabled = 40312
	// CodeProbeIMAPDisabled: the token was accepted but Exchange will not open the
	// mailbox ("User is authenticated but not connected"): IMAP is disabled for the
	// mailbox, or the signed-in account is not the mailbox owner.
	CodeProbeIMAPDisabled = 40313
)

func annotate(p *upstream.ProbePart) {
	if p.OK {
		return
	}
	d := strings.ToLower(p.Detail)
	switch {
	case strings.Contains(d, "5.7.139") || strings.Contains(d, "smtpclientauthentication is disabled"):
		p.Code, p.Msg = CodeProbeSMTPAuthDisabled, "smtp_auth_disabled"
	case strings.Contains(d, "authenticated but not connected"):
		p.Code, p.Msg = CodeProbeIMAPDisabled, "imap_access_disabled"
	}
}
