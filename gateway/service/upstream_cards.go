package service

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// upstreamErrorMsg is the public identifier of an upstream status code; the
// upstream server's own text stays in the admin's mailbox page and the logs.
var upstreamErrorMsg = map[int]string{
	jmapserver.CodeUpstreamAuthRequired: "auth_required",
	jmapserver.CodeUpstreamUnreachable:  "unreachable",
	jmapserver.CodeUpstreamRateLimited:  "rate_limited",
}

// ListUpstreams implements UpstreamSource for the overview: ownerPublicID's
// upstreams, or every user's for "".
func (s *Upstreams) ListUpstreams(ctx context.Context, ownerPublicID string) ([]UpstreamCard, error) {
	type row struct {
		model.Upstream
		OwnerPublicID string
		OwnerEmail    string
	}
	q := s.db.WithContext(ctx).Table("upstreams").
		Select("upstreams.*, users.public_id AS owner_public_id, users.email AS owner_email").
		Joins("JOIN users ON users.id = upstreams.user_id").
		Order("users.id, upstreams.id")
	if ownerPublicID != "" {
		q = q.Where("users.public_id = ?", ownerPublicID)
	}
	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	cards := make([]UpstreamCard, 0, len(rows))
	for i := range rows {
		cards = append(cards, s.card(&rows[i].Upstream, rows[i].OwnerEmail))
	}
	return cards, nil
}

func (s *Upstreams) card(up *model.Upstream, ownerEmail string) UpstreamCard {
	live := s.mgr.Status(up)
	c := UpstreamCard{
		ID: up.PublicID, Kind: up.Kind, Name: up.Name, Email: up.Email, OwnerEmail: ownerEmail,
		Status: live.State, Code: live.Code, WatchMode: watchMode(up),
	}
	if c.Status == "" {
		c.Status = UpstreamSyncing
	}
	if !live.Since.IsZero() {
		since := live.Since.UTC()
		c.StatusSince = &since
	}
	if !live.LastSync.IsZero() {
		at := live.LastSync.UTC()
		c.LastSyncAt = &at
	}
	if p := live.Progress; p != nil {
		c.InitialSync = &SyncProgress{Done: int64(p.Done), Total: int64(p.Total)}
	}
	if live.State == UpstreamAuthRequired || live.State == UpstreamUnreachable {
		at := time.Now().UTC()
		if c.StatusSince != nil {
			at = *c.StatusSince
		}
		msg := upstreamErrorMsg[live.Code]
		if msg == "" {
			msg = live.State
		}
		c.LastError = &UpstreamError{Code: live.Code, Msg: msg, At: at}
	}
	c.Proxy = s.proxyInfo(up)
	return c
}

// watchMode is how the gateway learns about changes, from the server's capabilities.
func watchMode(up *model.Upstream) string {
	if up.ServerCaps == "" {
		return ""
	}
	var caps upstream.Capabilities
	if json.Unmarshal([]byte(up.ServerCaps), &caps) != nil {
		return ""
	}
	switch {
	case caps.Notify:
		return "notify"
	case caps.Idle:
		return "idle"
	}
	return "poll"
}

// proxyInfo is the proxy an upstream connects through, nil for direct; never with credentials.
func (s *Upstreams) proxyInfo(up *model.Upstream) *ProxyInfo {
	switch up.ProxyMode {
	case model.ProxyNone:
		return nil
	case model.ProxyCustom:
		if p, err := netx.ParseProxyURL(up.ProxyURL); err == nil {
			return &ProxyInfo{Mode: "custom", Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
		}
		return &ProxyInfo{Mode: "custom"}
	}
	def := s.settings.String(SettingProxyDefault)
	if def == "" {
		return nil
	}
	if p, err := netx.ParseProxyURL(def); err == nil {
		return &ProxyInfo{Mode: "default", Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	}
	return &ProxyInfo{Mode: "default"}
}
