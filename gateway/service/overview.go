package service

import (
	"context"
	"errors"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/metrics"
)

// Upstream statuses, as in the JMAP account capability https://czl.net/jmap/upstream.
const (
	UpstreamOK           = "ok"
	UpstreamSyncing      = "syncing"
	UpstreamAuthRequired = "authRequired"
	UpstreamUnreachable  = "unreachable"
	UpstreamPaused       = "paused"
)

// UpstreamCard is one upstream mailbox on the overview.
type UpstreamCard struct {
	ID          string         `json:"id"`   // JMAP account id
	Kind        string         `json:"kind"` // imap, gmail, microsoft
	Name        string         `json:"name"`
	Email       string         `json:"email"`
	OwnerEmail  string         `json:"ownerEmail"` // the gateway user it belongs to
	Status      string         `json:"status"`
	StatusSince *time.Time     `json:"statusSince"`
	Code        int            `json:"code"`      // upstream code (40110, 50210, …) or 0
	WatchMode   string         `json:"watchMode"` // notify, idle, push, poll; "" while unknown
	LastSyncAt  *time.Time     `json:"lastSyncAt"`
	InitialSync *SyncProgress  `json:"initialSync"` // null once the first import is done
	LastError   *UpstreamError `json:"lastError"`
	Proxy       *ProxyInfo     `json:"proxy"` // null: direct connection
}

// SyncProgress is the first import's progress in messages.
type SyncProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// UpstreamError is the last failure talking to an upstream. Msg is the gateway's own
// English identifier, never the upstream server's reply.
type UpstreamError struct {
	Code int       `json:"code"`
	Msg  string    `json:"msg"`
	At   time.Time `json:"at"`
}

// ProxyInfo says how an upstream connects. Host never includes credentials.
type ProxyInfo struct {
	Mode string `json:"mode"` // default (the gateway's default proxy) or custom
	Host string `json:"host"`
}

// UpstreamSource lists upstream mailboxes for the overview. ownerPublicID "" lists
// every user's. The upstream framework (#39) implements it.
type UpstreamSource interface {
	ListUpstreams(ctx context.Context, ownerPublicID string) ([]UpstreamCard, error)
}

// OAuthSecret is an OAuth client secret with an expiry date (Microsoft's expire).
type OAuthSecret struct {
	Provider  string    `json:"provider"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// OAuthSecretSource lists the OAuth client secrets that have an expiry date. The
// Microsoft upstream (#43) implements it.
type OAuthSecretSource interface {
	OAuthSecrets(ctx context.Context) ([]OAuthSecret, error)
}

// Overview scopes.
const (
	ScopeMine = "mine"
	ScopeAll  = "all"
)

// SecretExpiryWarning is how early an expiring OAuth secret becomes a to-do.
const SecretExpiryWarning = 30 * 24 * time.Hour

// trendTop is how many upstreams get their own trend series; the rest merge.
const trendTop = 5

// Todo kinds.
const (
	TodoReauth          = "reauth"
	TodoSecretExpiring  = "oauthSecretExpiring"
	TodoMasterKeyBackup = "masterKeyBackup"
)

// Todo is something an admin or user should act on.
type Todo struct {
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"` // danger, warning
	UpstreamID string     `json:"upstreamId,omitempty"`
	Name       string     `json:"name,omitempty"` // upstream name or provider
	DueAt      *time.Time `json:"dueAt,omitempty"`
}

// OverviewView is the admin overview: upstream cards, online clients, to-dos and the trend.
type OverviewView struct {
	GeneratedAt   time.Time      `json:"generatedAt"`
	Scope         string         `json:"scope"`
	Upstreams     []UpstreamCard `json:"upstreams"`
	StatusCounts  map[string]int `json:"statusCounts"`
	OnlineClients int            `json:"onlineClients"`
	Todos         []Todo         `json:"todos"`
	Trend         *metrics.Trend `json:"trend"`
}

// ErrInvalidScope is an overview scope other than mine / all.
var ErrInvalidScope = errors.New("invalid scope")

// ErrInvalidRange is a trend range not in metrics.Ranges.
var ErrInvalidRange = errors.New("invalid range")

type noUpstreams struct{}

func (noUpstreams) ListUpstreams(context.Context, string) ([]UpstreamCard, error) { return nil, nil }

// Overview assembles the overview page. Upstreams and Secrets may be nil until the
// upstream packages provide them.
type Overview struct {
	Upstreams UpstreamSource
	Secrets   OAuthSecretSource
	Metrics   *metrics.Recorder
	System    *System
	now       func() time.Time
}

// Get returns the overview. Scope "" is "all" for admins and "mine" for users;
// only admins may ask for "all".
func (o *Overview) Get(ctx context.Context, actor *model.User, scope, rangeName string) (*OverviewView, error) {
	admin := IsAdmin(actor)
	switch scope {
	case "":
		scope = ScopeMine
		if admin {
			scope = ScopeAll
		}
	case ScopeAll:
		if !admin {
			return nil, ErrForbidden
		}
	case ScopeMine:
	default:
		return nil, ErrInvalidScope
	}
	if rangeName == "" {
		rangeName = "24h"
	}
	if _, ok := metrics.Ranges[rangeName]; !ok {
		return nil, ErrInvalidRange
	}
	src := o.Upstreams
	if src == nil {
		src = noUpstreams{}
	}
	owner := actor.PublicID
	if scope == ScopeAll {
		owner = ""
	}
	cards, err := src.ListUpstreams(ctx, owner)
	if err != nil {
		return nil, err
	}
	if cards == nil {
		cards = []UpstreamCard{}
	}
	now := time.Now().UTC()
	if o.now != nil {
		now = o.now().UTC()
	}
	v := &OverviewView{GeneratedAt: now, Scope: scope, Upstreams: cards, StatusCounts: map[string]int{}, Todos: []Todo{}}
	ids := make([]string, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.ID)
		v.StatusCounts[c.Status]++
		if c.Status == UpstreamAuthRequired {
			v.Todos = append(v.Todos, Todo{Kind: TodoReauth, Severity: "danger", UpstreamID: c.ID, Name: c.Name})
		}
	}
	if o.System != nil {
		v.OnlineClients = o.System.streams(owner)
	}
	if admin {
		todos, err := o.adminTodos(ctx, now)
		if err != nil {
			return nil, err
		}
		v.Todos = append(v.Todos, todos...)
	}
	if v.Trend, err = o.Metrics.Trend(ids, rangeName, trendTop); err != nil {
		return nil, err
	}
	return v, nil
}

func (o *Overview) adminTodos(ctx context.Context, now time.Time) ([]Todo, error) {
	var out []Todo
	if o.Secrets != nil {
		secrets, err := o.Secrets.OAuthSecrets(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range secrets {
			if s.ExpiresAt.Sub(now) > SecretExpiryWarning {
				continue
			}
			sev := "warning"
			if !s.ExpiresAt.After(now) {
				sev = "danger"
			}
			due := s.ExpiresAt.UTC()
			out = append(out, Todo{Kind: TodoSecretExpiring, Severity: sev, Name: s.Provider, DueAt: &due})
		}
	}
	if o.System != nil {
		at, err := o.System.MasterKeyBackupConfirmedAt()
		if err != nil {
			return nil, err
		}
		if at == nil {
			out = append(out, Todo{Kind: TodoMasterKeyBackup, Severity: "warning"})
		}
	}
	return out, nil
}
