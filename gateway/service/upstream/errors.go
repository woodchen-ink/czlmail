package upstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
)

// ErrorKind classifies upstream failures.
type ErrorKind int

// Error kinds.
const (
	// KindUnreachable: network, proxy, TLS, timeout, server 5xx or BYE. Retried with backoff.
	KindUnreachable ErrorKind = iota
	// KindAuth: the server explicitly rejected the credentials. Not retried.
	KindAuth
	// KindThrottled: the provider limits the gateway ([LIMIT], THROTTLED). Retried slowly.
	KindThrottled
	// KindRejected: the server refused one operation (NO: folder exists, quota,
	// unknown message). The connection is fine; the caller reports a SetError.
	KindRejected
)

// Error is a classified upstream failure. Err keeps the original for logs and the
// admin; clients only ever see the public code.
type Error struct {
	Kind ErrorKind
	Op   string
	Err  error
	// Code, when set, is the public status code instead of the kind's default
	// (a provider-specific condition the admin can act on, e.g. 40313).
	Code int
}

func (e *Error) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Status is the jmapserver upstream status for the kind.
func (e *Error) Status() (string, int) {
	state, code := jmapserver.UpstreamUnreachable, jmapserver.CodeUpstreamUnreachable
	switch e.Kind {
	case KindAuth:
		state, code = jmapserver.UpstreamAuthRequired, jmapserver.CodeUpstreamAuthRequired
	case KindThrottled:
		code = jmapserver.CodeUpstreamRateLimited
	}
	if e.Code != 0 {
		code = e.Code
	}
	return state, code
}

// Classify wraps err as an *Error (keeping an existing one).
func Classify(op string, err error) *Error {
	if err == nil {
		return nil
	}
	var ue *Error
	if errors.As(err, &ue) {
		return ue
	}
	return &Error{Kind: kindOf(err), Op: op, Err: err}
}

// IsKind reports whether err is an *Error of kind k.
func IsKind(err error, k ErrorKind) bool {
	var ue *Error
	return errors.As(err, &ue) && ue.Kind == k
}

func kindOf(err error) ErrorKind {
	var se *imapc.StatusError
	if errors.As(err, &se) {
		return imapKind(se)
	}
	var tp *textproto.Error
	if errors.As(err, &tp) {
		switch {
		case tp.Code == 535 || tp.Code == 534:
			return KindAuth
		case tp.Code == 421 || tp.Code == 450 || tp.Code == 451 || tp.Code == 452 || tp.Code == 454:
			if containsAny(tp.Msg, "rate", "too many", "throttl") {
				return KindThrottled
			}
			return KindUnreachable
		case tp.Code >= 500:
			return KindRejected
		}
		return KindUnreachable
	}
	var ue x509.UnknownAuthorityError
	var he x509.HostnameError
	var ce *tls.CertificateVerificationError
	if errors.As(err, &ue) || errors.As(err, &he) || errors.As(err, &ce) {
		return KindUnreachable
	}
	return KindUnreachable
}

// imapKind maps a NO / BAD / BYE answer. Only an explicit rejection of the
// sign-in is KindAuth; anything ambiguous is retried.
func imapKind(se *imapc.StatusError) ErrorKind {
	code := strings.ToUpper(se.Code)
	switch code {
	case "AUTHENTICATIONFAILED", "AUTHORIZATIONFAILED", "EXPIRED":
		return KindAuth
	case "LIMIT", "THROTTLED", "OVERQUOTA":
		if code == "OVERQUOTA" {
			return KindRejected
		}
		return KindThrottled
	case "UNAVAILABLE", "INUSE", "SERVERBUG":
		return KindUnreachable
	}
	if containsAny(se.Text, "throttl", "too many simultaneous", "rate limit", "bandwidth limit") {
		return KindThrottled
	}
	// Exchange Online accepts the token, then answers every command with this
	// while it cannot open the mailbox: IMAP is off for it, or — seen right after
	// a personal account's first authorisation — the mailbox is not ready yet. A
	// new sign-in would not help either way, so it is retried with backoff (the
	// status detail and the connection test, 40313, say what it is).
	if containsAny(se.Text, "authenticated but not connected") {
		return KindUnreachable
	}
	if se.Status == "BYE" {
		return KindUnreachable
	}
	if se.Command == "LOGIN" || se.Command == "AUTHENTICATE" {
		if se.Status == "NO" {
			return KindAuth
		}
		return KindUnreachable
	}
	if se.Status == "NO" {
		return KindRejected
	}
	return KindUnreachable // BAD: a gateway bug or a server oddity; retry later
}

func containsAny(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// Public codes for connection tests, by failure.
const (
	CodeProbeAuth        = 40110
	CodeProbeUnreachable = 50210
	CodeProbeProxy       = 50211
	CodeProbeTLS         = 50212
	CodeProbePrivate     = 40311
	CodeProbeThrottled   = 42910
	CodeProbeRejected    = 50213
)

// ProbeFailure fills a ProbePart for err.
func ProbeFailure(err error) ProbePart {
	var pe *netx.ProxyError
	var ce *tls.CertificateVerificationError
	var re tls.RecordHeaderError
	var ne net.Error
	p := ProbePart{Detail: err.Error()}
	switch {
	case errors.Is(err, netx.ErrPrivateAddress):
		p.Code, p.Msg = CodeProbePrivate, "private_network_not_allowed"
	case errors.As(err, &pe):
		p.Code, p.Msg = CodeProbeProxy, "proxy_failed"
	case errors.As(err, &ce), errors.As(err, &re):
		p.Code, p.Msg = CodeProbeTLS, "tls_failed"
	case IsKind(Classify("", err), KindAuth):
		p.Code, p.Msg = CodeProbeAuth, "authentication_failed"
	case IsKind(Classify("", err), KindThrottled):
		p.Code, p.Msg = CodeProbeThrottled, "rate_limited"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		p.Code, p.Msg = CodeProbeUnreachable, "timeout"
	case errors.Is(err, io.EOF):
		p.Code, p.Msg = CodeProbeUnreachable, "connection_closed"
	case IsKind(Classify("", err), KindRejected):
		p.Code, p.Msg = CodeProbeRejected, "server_rejected"
	default:
		p.Code, p.Msg = CodeProbeUnreachable, "unreachable"
	}
	return p
}
