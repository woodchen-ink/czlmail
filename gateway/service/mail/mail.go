// Package mail implements JMAP Mail (RFC 8621) over the upstream index: Mailbox,
// Email, Thread and SearchSnippet methods, and the m / p blob sources.
//
// Reads come from each upstream's mailindex and keep working while the upstream
// is unreachable; bodies, headers and server-side searches go to the upstream on
// demand (through an on-disk cache), and writes go to the upstream first. A call
// that needs an upstream which cannot serve it fails with serverUnavailable and a
// czlUpstream member saying why.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Capability is the JMAP Mail capability every method here requires.
const Capability = service.MailCapability

// Upstreams finds the running upstream of an account (service.UpstreamManager).
type Upstreams interface {
	Lookup(accountID string) (*service.Handle, *jmapserver.UpstreamError)
}

// Options configure the service.
type Options struct {
	Upstreams Upstreams
	Log       *slog.Logger
	// Uploads resolves uploaded blobs for Email/import and Email/set create.
	Uploads *jmapserver.UploadStore
	// Cache keeps fetched headers and parts; nil fetches every time.
	Cache *Cache
	// OnNewMail receives emails delivered into an upstream (for push); optional.
	OnNewMail func(userID string, mails []NewMail)
}

// Service holds the Mail methods.
type Service struct {
	opts  Options
	log   *slog.Logger
	cache *Cache

	deliveries sync.WaitGroup // new-mail notifications in flight
}

// New returns the service; Register adds its methods to a JMAP server.
func New(opts Options) (*Service, error) {
	s := &Service{opts: opts, log: opts.Log, cache: opts.Cache}
	if s.cache == nil {
		c, err := NewCache(filepath.Join(os.TempDir(), "czlmail-gateway-fetch"), 0)
		if err != nil {
			return nil, err
		}
		s.cache = c
	}
	return s, nil
}

// Register adds the Mail methods and the m / p blob sources.
func (s *Service) Register(srv *jmapserver.Server) {
	reg := func(name string, fn jmapserver.MethodFunc) { srv.Register(name, Capability, fn) }
	reg("Mailbox/get", s.mailboxGet)
	reg("Mailbox/changes", s.mailboxChanges)
	reg("Mailbox/query", s.mailboxQuery)
	reg("Mailbox/queryChanges", queryChanges)
	reg("Mailbox/set", s.mailboxSet)
	reg("Thread/get", s.threadGet)
	reg("Thread/changes", s.threadChanges)
	reg("Email/get", s.emailGet)
	reg("Email/changes", s.emailChanges)
	reg("Email/query", s.emailQuery)
	reg("Email/queryChanges", queryChanges)
	reg("Email/set", s.emailSet)
	reg("Email/import", s.emailImport)
	reg("SearchSnippet/get", s.snippetGet)
	srv.RegisterBlobSource(blobPrefixMessage, blobSource{s})
	srv.RegisterBlobSource(blobPrefixPart, blobSource{s})
}

// account is one call's view of an upstream account.
type account struct {
	id  string
	up  upstream.Upstream
	idx *mailindex.Store
	// down is set when the upstream cannot serve calls; the index still answers reads.
	down *jmapserver.UpstreamError
}

// open checks the account and finds its upstream. The index must be ready;
// before the first sync finished every Mail method gets serverUnavailable.
func (s *Service) open(call *jmapserver.Call, accountID string) (*account, error) {
	if _, err := call.Account(accountID); err != nil {
		return nil, err
	}
	return s.lookup(accountID)
}

func (s *Service) lookup(accountID string) (*account, error) {
	h, uerr := s.opts.Upstreams.Lookup(accountID)
	if h == nil {
		if uerr == nil {
			uerr = &jmapserver.UpstreamError{Status: jmapserver.UpstreamUnreachable, Code: jmapserver.CodeUpstreamUnreachable}
		}
		return nil, jmapserver.ServerUnavailable(uerr)
	}
	return &account{id: accountID, up: h.Upstream, idx: h.Upstream.Index(), down: uerr}, nil
}

// online fails with serverUnavailable when the upstream cannot serve calls.
func (a *account) online() error {
	if a.down != nil {
		return jmapserver.ServerUnavailable(a.down)
	}
	return nil
}

// state is the current state of typ (the index is ready once open succeeded).
func (a *account) state(typ string) string {
	st, _ := a.idx.State(typ)
	return st
}

// unavailable converts an upstream failure into serverUnavailable. Rejections
// (the server refused this one operation) are not unavailability: ok is false
// and the caller reports a SetError instead.
func unavailable(err error) (*jmapserver.MethodError, bool) {
	var ue *upstream.Error
	if errors.As(err, &ue) {
		if ue.Kind == upstream.KindRejected {
			return nil, false
		}
		st, code := ue.Status()
		return jmapserver.ServerUnavailable(&jmapserver.UpstreamError{Status: st, Code: code}), true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return jmapserver.ServerUnavailable(&jmapserver.UpstreamError{Status: jmapserver.UpstreamUnreachable, Code: jmapserver.CodeUpstreamUnreachable}), true
	}
	return nil, false
}

// isNotFound reports an upstream answer meaning "no such message or mailbox".
func isNotFound(err error) bool { return errors.Is(err, upstream.ErrNotFound) }

// queryChanges answers every /queryChanges: query results come from the index
// and from server searches, whose history is not kept.
func queryChanges(_ context.Context, call *jmapserver.Call) (any, error) {
	var args struct {
		AccountID string `json:"accountId"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if _, err := call.Account(args.AccountID); err != nil {
		return nil, err
	}
	return nil, jmapserver.ErrCannotCalculateChanges
}
