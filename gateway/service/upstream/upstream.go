// Package upstream defines what the gateway needs from one upstream mailbox and
// runs it: the Upstream interface, its configuration, error classes, and the
// Runner that drives sync, watching, backoff and the status state machine.
//
// Implementations live in subpackages (imap; gmail and microsoft reuse imap with
// their own authentication and dialect) and register a Factory per kind.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// Kinds of upstream.
const (
	KindIMAP      = "imap"
	KindGmail     = "gmail"
	KindMicrosoft = "microsoft"
)

// Endpoint is one server of an upstream.
type Endpoint struct {
	Host     string
	Port     int
	TLS      string // imapc.TLSImplicit, TLSStartTLS, TLSNone
	Username string
	Password string
}

// Config is everything an implementation needs to reach one upstream. Secrets are
// already decrypted; Dialer already carries the proxy and the private-network guard.
type Config struct {
	AccountID string
	Kind      string
	Email     string
	IMAP      Endpoint
	SMTP      Endpoint
	Dialer    netx.Dialer
	// AccessToken returns a current OAuth access token for XOAUTH2 (gmail,
	// microsoft; filled in by #42 / #43). An *Error of KindAuth means the grant is
	// gone. Nil for password upstreams.
	AccessToken func(ctx context.Context) (string, error)
	// APIBase is the provider's REST API root for OAuth kinds (the Gmail API
	// sends mail and lists identities); it goes through Dialer too.
	APIBase string
	Log     *slog.Logger
	// OnRequest, if set, is called for every request sent to the upstream (an IMAP
	// command, an HTTP call); the admin overview counts them. Nil for connection tests.
	OnRequest func()
}

// Metrics records upstream activity for the admin overview (service/metrics.Recorder).
type Metrics interface {
	RateLimited(accountID string)
	PushLatency(accountID string, d time.Duration)
}

// Capabilities are what the server offers, as reported by Probe.
type Capabilities struct {
	CondStore  bool `json:"condstore"`
	QResync    bool `json:"qresync"`
	Idle       bool `json:"idle"`
	Notify     bool `json:"notify"`
	Move       bool `json:"move"`
	UIDPlus    bool `json:"uidplus"`
	ObjectID   bool `json:"objectid"`
	XGUID      bool `json:"xguid"`
	SpecialUse bool `json:"specialUse"`
	ListStatus bool `json:"listStatus"`
	Sort       bool `json:"sort"`
}

// SyncRequest selects what a sync round looks at.
type SyncRequest struct {
	// Mailboxes are folder names the watcher reported as changed.
	Mailboxes []string
	// Full checks every folder now (first sync, a client connected, a reconnect).
	Full bool
	// Deep re-reads every UID and flag (nightly compare for servers without CONDSTORE).
	Deep bool
	// Idle is true when the user has had no push connection for a while: polling
	// slows down.
	Idle bool
	// Progress reports the first import, in messages.
	Progress func(done, total int)
}

// ErrNotFound means an email or mailbox id is not in the index, or the message
// is gone from the server. Implementations wrap it in an *Error of KindRejected.
var ErrNotFound = errors.New("upstream: not found")

// SearchQuery holds the search criteria the index cannot answer; every non-empty
// field must match (AND). Values are matched as substrings, case-insensitively,
// as IMAP SEARCH does.
type SearchQuery struct {
	Text []string // anywhere in the headers or body
	Body []string // in the body
	Bcc  []string
	// Header lists [name] (the header exists) or [name, value] (it contains value).
	Header [][]string
}

// Hint is a change the watcher saw. Mailbox "" means "something, check all".
type Hint struct{ Mailbox string }

// Upstream is one connected mailbox. Methods may be called concurrently; an
// implementation limits its own connections.
type Upstream interface {
	// Probe signs in and reports the server's capabilities.
	Probe(ctx context.Context) (*Capabilities, error)
	// Sync runs one round and returns the new states of the types that changed.
	// The first round that completes with the full mailbox imported marks the
	// index ready.
	Sync(ctx context.Context, req SyncRequest) (map[string]string, error)
	// Watch holds the listening connection, calling hint for changes, until ctx
	// ends (nil) or the connection fails.
	Watch(ctx context.Context, hint func(Hint)) error
	// Index is the upstream's index.
	Index() *mailindex.Store

	// FetchRaw streams the whole message.
	FetchRaw(ctx context.Context, emailID string, w io.Writer) error
	// FetchSection streams one MIME part (IMAP section, e.g. "1.2"), still in its
	// transfer encoding as stored; the part's encoding is in the body structure.
	FetchSection(ctx context.Context, emailID, section string, w io.Writer) error

	// Search runs a server-side search in one mailbox and returns the matching
	// email ids (only those in the index). The caller fans out over mailboxes.
	Search(ctx context.Context, mailboxID string, q SearchQuery) ([]string, error)

	// Writes go to the server first and to the index right after; the returned
	// states are the new states of the changed types.
	SetKeywords(ctx context.Context, emailIDs []string, add, remove []string) (map[string]string, error)
	Move(ctx context.Context, emailIDs []string, toMailboxID string) (map[string]string, error)
	Copy(ctx context.Context, emailIDs []string, toMailboxID string) ([]string, map[string]string, error)
	Append(ctx context.Context, mailboxID string, raw []byte, keywords []string, receivedAt time.Time) (string, map[string]string, error)
	Destroy(ctx context.Context, emailIDs []string) (map[string]string, error)
	CreateMailbox(ctx context.Context, parentID, name string) (string, map[string]string, error)
	RenameMailbox(ctx context.Context, mailboxID, parentID, name string) (map[string]string, error)
	DeleteMailbox(ctx context.Context, mailboxID string) (map[string]string, error)

	// Close drops every connection.
	Close() error
}

// Factory builds an Upstream of one kind.
type Factory func(cfg Config, index *mailindex.Store) (Upstream, error)

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
)

// Register makes a kind available; call it from an init function.
func Register(kind string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[kind] = f
}

// New builds the Upstream for cfg.Kind.
func New(cfg Config, index *mailindex.Store) (Upstream, error) {
	regMu.RLock()
	f := registry[cfg.Kind]
	regMu.RUnlock()
	if f == nil {
		return nil, fmt.Errorf("upstream: kind %q is not available", cfg.Kind)
	}
	return f(cfg, index)
}

// Prober tests a connection without an index ("test connection" in the admin).
type Prober func(ctx context.Context, cfg Config) (*ProbeReport, error)

// ProbeReport is the outcome of a connection test.
type ProbeReport struct {
	IMAP ProbePart  `json:"imap"`
	SMTP *ProbePart `json:"smtp"` // nil when no SMTP server is configured
}

// ProbePart is one server's test result. Code and Msg follow the public error
// scheme; Detail is the server's own reply, for the admin only.
type ProbePart struct {
	OK           bool          `json:"ok"`
	Code         int           `json:"code,omitempty"`
	Msg          string        `json:"msg,omitempty"`
	Detail       string        `json:"detail,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty"`
	Auth         []string      `json:"auth,omitempty"`
}

var (
	probeMu sync.RWMutex
	probers = map[string]Prober{}
)

// RegisterProber makes connection tests available for a kind.
func RegisterProber(kind string, p Prober) {
	probeMu.Lock()
	defer probeMu.Unlock()
	probers[kind] = p
}

// Probe tests cfg's servers.
func Probe(ctx context.Context, cfg Config) (*ProbeReport, error) {
	probeMu.RLock()
	p := probers[cfg.Kind]
	probeMu.RUnlock()
	if p == nil {
		return nil, fmt.Errorf("upstream: kind %q cannot be tested", cfg.Kind)
	}
	return p(ctx, cfg)
}
