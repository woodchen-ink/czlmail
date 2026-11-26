// Package imap is the IMAP upstream: incremental sync in three tiers (QRESYNC,
// CONDSTORE, plain), watching (NOTIFY, IDLE, polling), on-demand body fetches and
// write operations, all kept in the upstream's mailindex.
//
// Gmail and Microsoft reuse it through NewWithOptions with their own sign-in and
// Dialect.
package imap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

func init() {
	upstream.Register(upstream.KindIMAP, func(cfg upstream.Config, idx *mailindex.Store) (upstream.Upstream, error) {
		return NewWithOptions(cfg, idx, Options{}), nil
	})
	upstream.RegisterProber(upstream.KindIMAP, Probe)
}

// Dialect adapts the engine to a provider (Gmail: X-GM-MSGID as the stable key,
// X-GM-THRID as the thread). The zero value is plain IMAP.
type Dialect struct {
	// FetchItems are extra FETCH items requested with the metadata.
	FetchItems []string
	// StableKey returns the provider's stable message key, "" for none.
	StableKey func(m *imapc.Message) string
	// ThreadKey returns the provider's thread key, "" for none.
	ThreadKey func(m *imapc.Message) string
	// SkipMailbox hides folders (Gmail's [Gmail] parent).
	SkipMailbox func(e *imapc.ListEntry) bool
	// SearchCriteria builds the UID SEARCH criteria for a query (Gmail: one
	// X-GM-RAW string); nil uses the standard SEARCH keys. utf8 asks for
	// CHARSET UTF-8 in front of them.
	SearchCriteria func(q upstream.SearchQuery) (criteria []any, utf8 bool)
}

// Options configure an IMAP upstream beyond Config.
type Options struct {
	// SignIn authenticates a fresh connection. Default: XOAUTH2 when
	// Config.AccessToken is set, else LOGIN with the IMAP username and password.
	SignIn  func(ctx context.Context, c *imapc.Client, cfg upstream.Config) error
	Dialect Dialect
	// WorkConns caps connections besides the watcher (default 2).
	WorkConns int
	// Clock is for tests.
	Now func() time.Time
}

// Upstream is one IMAP mailbox.
type Upstream struct {
	cfg  upstream.Config
	opts Options
	idx  *mailindex.Store
	log  *slog.Logger

	pool   *pool
	bucket *bucket

	// op serialises sync rounds and writes, so the index never interleaves a
	// round's view of a folder with the gateway's own changes to it.
	op sync.Mutex

	mu    sync.Mutex
	caps  serverCaps
	sched schedule
}

// serverCaps is what the engine uses, learned on the first connection.
type serverCaps struct {
	known      bool
	qresync    bool
	condstore  bool
	notify     bool
	idle       bool
	listStatus bool
	objectID   bool
	xguid      bool
	move       bool
	uidplus    bool
	specialUse bool
	// noBodyStructure is learned when the server answers BAD to BODYSTRUCTURE.
	noBodyStructure bool
}

func (c serverCaps) tier() string {
	switch {
	case c.qresync:
		return "qresync"
	case c.condstore:
		return "condstore"
	}
	return "basic"
}

// NewWithOptions returns an IMAP upstream over idx.
func NewWithOptions(cfg upstream.Config, idx *mailindex.Store, opts Options) *Upstream {
	if opts.WorkConns <= 0 {
		opts.WorkConns = 2
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SignIn == nil {
		opts.SignIn = defaultSignIn
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	u := &Upstream{cfg: cfg, opts: opts, idx: idx, log: log.With("account", cfg.AccountID), bucket: newBucket(3, 20*time.Second)}
	u.pool = newPool(opts.WorkConns, u.dial)
	u.sched.init()
	return u
}

func defaultSignIn(ctx context.Context, c *imapc.Client, cfg upstream.Config) error {
	if cfg.AccessToken != nil {
		tok, err := cfg.AccessToken(ctx)
		if err != nil {
			return err
		}
		return c.LoginXOAuth2(ctx, cfg.IMAP.Username, tok)
	}
	return c.Login(ctx, cfg.IMAP.Username, cfg.IMAP.Password)
}

// Index is the upstream's index.
func (u *Upstream) Index() *mailindex.Store { return u.idx }

// Close drops every connection.
func (u *Upstream) Close() error {
	u.pool.close()
	return nil
}

// dial opens and signs in a new connection, waiting for the connection token bucket.
func (u *Upstream) dial(ctx context.Context) (*imapc.Client, error) {
	return u.dialWith(ctx, nil)
}

func (u *Upstream) dialWith(ctx context.Context, unsolicited func(*imapc.Response)) (*imapc.Client, error) {
	if err := u.bucket.wait(ctx); err != nil {
		return nil, err
	}
	c, err := connect(ctx, u.cfg, u.opts, unsolicited)
	if err != nil {
		return nil, err
	}
	u.learnCaps(ctx, c)
	return c, nil
}

// connect dials, signs in and enables the extensions the engine uses.
func connect(ctx context.Context, cfg upstream.Config, opts Options, unsolicited func(*imapc.Response)) (*imapc.Client, error) {
	addr := net.JoinHostPort(cfg.IMAP.Host, strconv.Itoa(cfg.IMAP.Port))
	var onCommand func(string)
	if cfg.OnRequest != nil {
		onCommand = func(string) { cfg.OnRequest() }
	}
	c, err := imapc.Dial(ctx, cfg.Dialer.DialContext, addr, imapc.Options{TLS: cfg.IMAP.TLS, Unsolicited: unsolicited, OnCommand: onCommand})
	if err != nil {
		return nil, upstream.Classify("connect", err)
	}
	if err := opts.SignIn(ctx, c, cfg); err != nil {
		c.Close()
		return nil, upstream.Classify("sign in", err)
	}
	if _, err := c.Caps(ctx); err != nil {
		c.Close()
		return nil, upstream.Classify("capability", err)
	}
	c.ID(ctx, "czlmail-gateway", "1")
	// QRESYNC implies CONDSTORE; enabling both is allowed.
	if err := c.Enable(ctx, "QRESYNC", "CONDSTORE"); err != nil {
		c.Close()
		return nil, upstream.Classify("enable", err)
	}
	return c, nil
}

func (u *Upstream) learnCaps(ctx context.Context, c *imapc.Client) {
	u.mu.Lock()
	known := u.caps.known
	u.mu.Unlock()
	if known {
		return
	}
	sc := serverCaps{
		known:      true,
		qresync:    c.Has("QRESYNC") && c.Enabled("QRESYNC"),
		condstore:  c.Has("CONDSTORE"),
		notify:     c.Has("NOTIFY"),
		idle:       c.Has("IDLE"),
		listStatus: c.Has("LIST-STATUS"),
		objectID:   c.Has("OBJECTID"),
		move:       c.Has("MOVE"),
		uidplus:    c.Has("UIDPLUS"),
		specialUse: c.Has("SPECIAL-USE"),
	}
	if !sc.objectID {
		sc.xguid = probeXGUID(ctx, c)
	}
	u.mu.Lock()
	sc.noBodyStructure = u.caps.noBodyStructure
	u.caps = sc
	u.mu.Unlock()
	u.log.Info("upstream capabilities", "tier", sc.tier(), "notify", sc.notify, "idle", sc.idle, "objectid", sc.objectID, "xguid", sc.xguid, "list_status", sc.listStatus)
}

// probeXGUID asks for Dovecot's X-GUID; other servers answer BAD for the unknown item.
func probeXGUID(ctx context.Context, c *imapc.Client) bool {
	if _, err := c.Select(ctx, "INBOX", imapc.SelectOptions{ReadOnly: true}); err != nil {
		return false
	}
	defer c.Unselect(ctx)
	err := c.Fetch(ctx, "1:*", []string{"UID", "X-GUID"}, imapc.FetchOptions{UID: true}, nil, nil)
	return err == nil
}

func (u *Upstream) serverCaps() serverCaps {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.caps
}

// withConn runs fn on a work connection. A reused connection that turns out dead
// (the server timed it out) is replaced once.
func (u *Upstream) withConn(ctx context.Context, fn func(*imapc.Client) error) error {
	for attempt := 0; ; attempt++ {
		c, reused, release, err := u.pool.get(ctx)
		if err != nil {
			return err
		}
		err = fn(c)
		broken := err != nil && !isOperational(err)
		release(broken)
		if broken && reused && attempt == 0 && ctx.Err() == nil && isConnLoss(err) {
			continue
		}
		if err != nil {
			return upstream.Classify("imap", err)
		}
		return nil
	}
}

// isOperational reports errors that leave the connection usable (NO answers).
func isOperational(err error) bool {
	var se *imapc.StatusError
	if errors.As(err, &se) && se.Status == "NO" {
		return true
	}
	var ue *upstream.Error
	return errors.As(err, &ue) && ue.Kind == upstream.KindRejected
}

func isConnLoss(err error) bool {
	var se *imapc.StatusError
	if errors.As(err, &se) {
		return se.Status == "BYE"
	}
	var ne net.Error
	return errors.Is(err, imapc.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne)
}

// Probe signs in and reports the server's capabilities.
func (u *Upstream) Probe(ctx context.Context) (*upstream.Capabilities, error) {
	var caps *upstream.Capabilities
	err := u.withConn(ctx, func(c *imapc.Client) error {
		caps = capabilities(ctx, c)
		return nil
	})
	return caps, err
}

func capabilities(ctx context.Context, c *imapc.Client) *upstream.Capabilities {
	caps := &upstream.Capabilities{
		CondStore: c.Has("CONDSTORE"), QResync: c.Has("QRESYNC"), Idle: c.Has("IDLE"), Notify: c.Has("NOTIFY"),
		Move: c.Has("MOVE"), UIDPlus: c.Has("UIDPLUS"), ObjectID: c.Has("OBJECTID"), SpecialUse: c.Has("SPECIAL-USE"),
		ListStatus: c.Has("LIST-STATUS"), Sort: c.Has("SORT"),
	}
	if !caps.ObjectID {
		caps.XGUID = probeXGUID(ctx, c)
	}
	return caps
}
