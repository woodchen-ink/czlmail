// Package jmapserver is a JMAP Core (RFC 8620) server engine: the Session resource,
// method dispatch with result references, blob upload and download, and EventSource push.
//
// It knows nothing about mail. Authentication, the accounts of a user and the methods
// are injected: an Authenticator, an AccountSource, Register for each method and
// RegisterBlobSource for each kind of downloadable blob.
package jmapserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Core limits announced in the Session, aligned with Stalwart's defaults.
const (
	MaxSizeUpload         = 50_000_000
	MaxConcurrentUpload   = 4
	MaxSizeRequest        = 10_000_000
	MaxConcurrentRequests = 4
	MaxCallsInRequest     = 16
	MaxObjectsInGet       = 500
	MaxObjectsInSet       = 500
)

// CoreCapability is the URI every request must use.
const CoreCapability = "urn:ietf:params:jmap:core"

// DefaultPushDebounce is how long changes are collected before one StateChange is sent.
const DefaultPushDebounce = 500 * time.Millisecond

// Authenticator identifies the user of a request. Only an error here makes the
// server answer 401; per-account problems must never surface as 401, because the
// client then treats the whole sign-in as revoked.
type Authenticator interface {
	Authenticate(r *http.Request) (userID string, err error)
}

// AccountSource lists the accounts a user can see.
type AccountSource interface {
	UserSession(ctx context.Context, userID string) (*UserSession, error)
}

// Options configure a Server.
type Options struct {
	// BaseURL is the public origin without a trailing slash; Session URLs are built from it.
	BaseURL string
	// DataDir holds uploaded blobs under DataDir/blobs.
	DataDir  string
	Auth     Authenticator
	Accounts AccountSource
	Log      *slog.Logger
	// TraceID returns the request's trace id for logs; optional.
	TraceID func(context.Context) string
	// PushDebounce overrides DefaultPushDebounce; zero keeps the default.
	PushDebounce time.Duration
}

// Server serves the JMAP endpoints. Create it with New, register capabilities,
// methods and blob sources before serving, then mount its handlers.
type Server struct {
	opts    Options
	log     *slog.Logger
	caps    map[string]any
	methods map[string]method
	blobSrc map[byte]BlobSource
	uploads *UploadStore
	bus     *Bus
	reqs    *userLimiter
	upl     *userLimiter
	mu      sync.RWMutex
}

// New returns a Server with the core capability and Core/echo registered.
func New(opts Options) (*Server, error) {
	if opts.BaseURL == "" || opts.Auth == nil || opts.Accounts == nil || opts.Log == nil {
		return nil, fmt.Errorf("jmapserver: BaseURL, Auth, Accounts and Log are required")
	}
	uploads, err := NewUploadStore(filepath.Join(opts.DataDir, "blobs"))
	if err != nil {
		return nil, err
	}
	debounce := opts.PushDebounce
	if debounce <= 0 {
		debounce = DefaultPushDebounce
	}
	s := &Server{
		opts:    opts,
		log:     opts.Log,
		caps:    map[string]any{CoreCapability: coreCapability()},
		methods: map[string]method{},
		blobSrc: map[byte]BlobSource{},
		uploads: uploads,
		bus:     NewBus(debounce),
		reqs:    newUserLimiter(MaxConcurrentRequests),
		upl:     newUserLimiter(MaxConcurrentUpload),
	}
	s.Register("Core/echo", CoreCapability, echo)
	return s, nil
}

// RegisterCapability announces a server-level capability in the Session.
// Clients may only put registered capabilities in `using`.
func (s *Server) RegisterCapability(uri string, value any) {
	if value == nil {
		value = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps[uri] = value
}

// Changes is the per-user change bus feeding EventSource connections.
func (s *Server) Changes() *Bus { return s.bus }

// Uploads is the store of uploaded blobs, for methods that consume them.
func (s *Server) Uploads() *UploadStore { return s.uploads }

// EndUserStreams closes every EventSource connection of the user, so the client
// reconnects and re-reads the Session (after an upstream status change, or when
// the user is disabled). It returns how many connections were closed.
func (s *Server) EndUserStreams(userID string) int { return s.bus.EndUser(userID) }

// Close ends all EventSource connections; call it before http.Server.Shutdown,
// which otherwise waits for streams that never go idle.
func (s *Server) Close() { s.bus.CloseAll() }

func (s *Server) url(path string) string { return s.opts.BaseURL + path }

func (s *Server) traceID(ctx context.Context) string {
	if s.opts.TraceID == nil {
		return ""
	}
	return s.opts.TraceID(ctx)
}

func coreCapability() map[string]any {
	return map[string]any{
		"maxSizeUpload":         MaxSizeUpload,
		"maxConcurrentUpload":   MaxConcurrentUpload,
		"maxSizeRequest":        MaxSizeRequest,
		"maxConcurrentRequests": MaxConcurrentRequests,
		"maxCallsInRequest":     MaxCallsInRequest,
		"maxObjectsInGet":       MaxObjectsInGet,
		"maxObjectsInSet":       MaxObjectsInSet,
		"collationAlgorithms":   []string{"i;ascii-numeric", "i;ascii-casemap", "i;unicode-casemap"},
	}
}
