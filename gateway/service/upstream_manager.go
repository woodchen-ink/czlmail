package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"

	// Upstream kinds register themselves.
	_ "github.com/woodchen-ink/czlmail/gateway/service/upstream/imap"
)

// Changelog retention (the JMAP state window).
const (
	ChangelogMaxAge     = 30 * 24 * time.Hour
	ChangelogMaxEntries = 200_000
)

// Notifier is the part of the JMAP server the manager drives.
type Notifier interface {
	// Publish sends new states to the user's EventSource connections.
	Publish(userID string, changed jmapserver.Changes)
	// EndUserStreams closes them, so clients re-read the Session.
	EndUserStreams(userID string) int
}

// UpstreamManager runs one upstream.Runner per active upstream and connects them
// to the JMAP server: index commits are published, status changes end the
// user's push streams (the Session changed), push connections steer polling.
type UpstreamManager struct {
	db       *gorm.DB
	ring     *crypto.Keyring
	settings *Settings
	dataDir  string
	log      *slog.Logger
	timing   upstream.Timing

	mu       sync.Mutex
	ctx      context.Context
	notifier Notifier
	running  map[string]*managed // by accountId
	statuses map[string]string   // accountId → last status written, to detect changes
	clients  map[string]int      // user public id → push connections
	stopped  bool
}

type managed struct {
	row    model.Upstream
	userID string // user public id
	runner *upstream.Runner
	up     upstream.Upstream
	idx    *mailindex.Store
	cancel context.CancelFunc
	done   chan struct{}
}

// NewUpstreamManager returns a manager; Start launches the upstreams.
func NewUpstreamManager(db *gorm.DB, ring *crypto.Keyring, settings *Settings, dataDir string, log *slog.Logger, timing upstream.Timing) *UpstreamManager {
	m := &UpstreamManager{db: db, ring: ring, settings: settings, dataDir: dataDir, log: log, timing: timing,
		running: map[string]*managed{}, statuses: map[string]string{}, clients: map[string]int{}}
	settings.OnChange(m.settingsChanged)
	return m
}

// Run starts every upstream that is not paused and keeps them until ctx ends.
// Index files without an upstream row (left by a crash during deletion) are removed.
func (m *UpstreamManager) Run(ctx context.Context, notifier Notifier) error {
	m.mu.Lock()
	m.ctx, m.notifier = ctx, notifier
	m.mu.Unlock()
	var rows []model.Upstream
	if err := m.db.Order("id").Find(&rows).Error; err != nil {
		return err
	}
	known := map[string]bool{}
	for i := range rows {
		known[rows[i].PublicID] = true
		m.mu.Lock()
		m.statuses[rows[i].PublicID] = rows[i].Status
		m.mu.Unlock()
		m.Start(&rows[i])
	}
	m.removeStrayFiles(known)
	return nil
}

func (m *UpstreamManager) removeStrayFiles(known map[string]bool) {
	files, _ := filepath.Glob(filepath.Join(m.dataDir, "upstreams", "*.sqlite"))
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".sqlite")
		if !known[id] {
			m.log.Warn("removing index of a deleted upstream", "account", id)
			_ = mailindex.Remove(m.dataDir, id)
		}
	}
}

// Stop stops every runner and waits for them.
func (m *UpstreamManager) Stop() {
	m.mu.Lock()
	m.stopped = true
	list := make([]*managed, 0, len(m.running))
	for _, r := range m.running {
		list = append(list, r)
	}
	m.running = map[string]*managed{}
	m.mu.Unlock()
	for _, r := range list {
		r.stop()
	}
}

func (r *managed) stop() {
	r.cancel()
	<-r.done
	r.up.Close()
	r.idx.Close()
}

// Start runs an upstream (paused ones only get their status).
func (m *UpstreamManager) Start(row *model.Upstream) {
	userID, err := m.userPublicID(row.UserID)
	if err != nil {
		m.log.Error("upstream owner missing", "account", row.PublicID, "err", err)
		return
	}
	if row.Paused {
		m.record(row.PublicID, userID, upstream.Status{State: jmapserver.UpstreamPaused, Code: jmapserver.CodeUpstreamPaused, Since: time.Now().UTC()})
		return
	}
	m.mu.Lock()
	if m.stopped || m.ctx == nil || m.running[row.PublicID] != nil {
		m.mu.Unlock()
		return
	}
	parent := m.ctx
	clients := m.clients[userID]
	m.mu.Unlock()

	cfg, err := m.Config(row)
	if err != nil {
		m.log.Error("upstream config", "account", row.PublicID, "err", err)
		m.record(row.PublicID, userID, upstream.Status{State: jmapserver.UpstreamAuthRequired, Code: jmapserver.CodeUpstreamAuthRequired, Since: time.Now().UTC(), Error: err.Error()})
		return
	}
	idx, err := mailindex.Open(m.dataDir, row.PublicID)
	if err != nil {
		m.log.Error("open upstream index", "account", row.PublicID, "err", err)
		return
	}
	up, err := upstream.New(cfg, idx)
	if err != nil {
		idx.Close()
		m.log.Error("upstream kind", "account", row.PublicID, "err", err)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r := &managed{row: *row, userID: userID, up: up, idx: idx, cancel: cancel, done: make(chan struct{})}
	r.runner = upstream.NewRunner(row.PublicID, up, &runnerEvents{m: m, userID: userID}, m.log, m.timing)
	r.runner.SetClients(clients)
	m.mu.Lock()
	if m.stopped || m.running[row.PublicID] != nil {
		m.mu.Unlock()
		cancel()
		up.Close()
		idx.Close()
		return
	}
	m.running[row.PublicID] = r
	m.mu.Unlock()
	go func() {
		defer close(r.done)
		r.runner.Run(ctx)
	}()
}

// Restart stops and starts an upstream after its settings changed.
func (m *UpstreamManager) Restart(row *model.Upstream) {
	m.stopOne(row.PublicID)
	m.Start(row)
}

// Renamed refreshes the row a runner reports with (name, default) and tells clients.
func (m *UpstreamManager) Renamed(row *model.Upstream) {
	m.mu.Lock()
	r := m.running[row.PublicID]
	if r != nil {
		r.row = *row
	}
	m.mu.Unlock()
	if userID, err := m.userPublicID(row.UserID); err == nil {
		m.endStreams(userID)
	}
}

// Remove stops an upstream whose row was deleted and deletes its index.
func (m *UpstreamManager) Remove(row *model.Upstream) {
	m.stopOne(row.PublicID)
	m.mu.Lock()
	delete(m.statuses, row.PublicID)
	m.mu.Unlock()
	if err := mailindex.Remove(m.dataDir, row.PublicID); err != nil {
		m.log.Error("remove upstream index", "account", row.PublicID, "err", err)
	}
	if userID, err := m.userPublicID(row.UserID); err == nil {
		m.endStreams(userID)
	}
}

// RemoveUser stops and deletes the upstreams of a deleted user (rows are already gone).
func (m *UpstreamManager) RemoveUser(userPublicID string) {
	m.mu.Lock()
	var ids []string
	for id, r := range m.running {
		if r.userID == userPublicID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stopOne(id)
		_ = mailindex.Remove(m.dataDir, id)
	}
	var rows []model.Upstream
	if err := m.db.Find(&rows).Error; err == nil {
		known := map[string]bool{}
		for _, r := range rows {
			known[r.PublicID] = true
		}
		m.removeStrayFiles(known)
	}
}

func (m *UpstreamManager) stopOne(id string) {
	m.mu.Lock()
	r := m.running[id]
	delete(m.running, id)
	m.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// settingsChanged restarts upstreams whose connections depend on the changed settings.
func (m *UpstreamManager) settingsChanged(keys []string) {
	proxy, private := false, false
	for _, k := range keys {
		switch k {
		case SettingProxyDefault, SettingProxyDefaultPassword:
			proxy = true
		case SettingAllowPrivateNetworks:
			private = true
		}
	}
	if !proxy && !private {
		return
	}
	var rows []model.Upstream
	if err := m.db.Find(&rows).Error; err != nil {
		return
	}
	for i := range rows {
		if private || rows[i].ProxyMode == model.ProxyFollow {
			go m.Restart(&rows[i])
		}
	}
}

// SetClients is told how many push connections a user has (jmapserver.Bus observer).
func (m *UpstreamManager) SetClients(userPublicID string, n int) {
	m.mu.Lock()
	m.clients[userPublicID] = n
	var rs []*managed
	for _, r := range m.running {
		if r.userID == userPublicID {
			rs = append(rs, r)
		}
	}
	m.mu.Unlock()
	for _, r := range rs {
		r.runner.SetClients(n)
	}
}

// Status is the live status of an upstream, falling back to its stored one.
func (m *UpstreamManager) Status(row *model.Upstream) upstream.Status {
	if row.Paused {
		return upstream.Status{State: jmapserver.UpstreamPaused, Code: jmapserver.CodeUpstreamPaused, Since: row.StatusSince}
	}
	m.mu.Lock()
	r := m.running[row.PublicID]
	m.mu.Unlock()
	if r != nil {
		if st := r.runner.Status(); st.State != "" {
			return st
		}
	}
	return upstream.Status{State: row.Status, Code: row.StatusCode, Since: row.StatusSince, Error: row.LastError}
}

// Handle is a running upstream for JMAP methods.
type Handle struct {
	Upstream upstream.Upstream
	Status   upstream.Status
}

// Lookup returns the running upstream of accountID, or a *jmapserver.UpstreamError
// for serverUnavailable when it is paused, still on its first sync, or not running.
// Reads may still use Handle.Upstream.Index() when the upstream is unreachable but
// ready; that choice is the caller's.
func (m *UpstreamManager) Lookup(accountID string) (*Handle, *jmapserver.UpstreamError) {
	m.mu.Lock()
	r := m.running[accountID]
	last := m.statuses[accountID]
	m.mu.Unlock()
	if r == nil {
		// Not running: paused, or its settings could not be used.
		switch last {
		case jmapserver.UpstreamAuthRequired:
			return nil, &jmapserver.UpstreamError{Status: last, Code: jmapserver.CodeUpstreamAuthRequired}
		case jmapserver.UpstreamPaused:
			return nil, &jmapserver.UpstreamError{Status: last, Code: jmapserver.CodeUpstreamPaused}
		}
		return nil, &jmapserver.UpstreamError{Status: jmapserver.UpstreamUnreachable, Code: jmapserver.CodeUpstreamUnreachable}
	}
	st := r.runner.Status()
	if !r.idx.Ready() {
		return nil, &jmapserver.UpstreamError{Status: jmapserver.UpstreamSyncing, Code: jmapserver.CodeUpstreamSyncing}
	}
	h := &Handle{Upstream: r.up, Status: st}
	switch st.State {
	case jmapserver.UpstreamAuthRequired, jmapserver.UpstreamUnreachable, jmapserver.UpstreamPaused:
		return h, &jmapserver.UpstreamError{Status: st.State, Code: st.Code}
	}
	return h, nil
}

// Trim prunes every running upstream's changelog (cron).
func (m *UpstreamManager) Trim() (int64, error) {
	m.mu.Lock()
	list := make([]*managed, 0, len(m.running))
	for _, r := range m.running {
		list = append(list, r)
	}
	m.mu.Unlock()
	var total int64
	var firstErr error
	for _, r := range list {
		n, err := r.idx.Trim(ChangelogMaxAge, ChangelogMaxEntries)
		total += n
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.row.PublicID, err)
		}
	}
	return total, firstErr
}

// Config builds an upstream.Config from a row: decrypted secrets and the dialer
// with the upstream's proxy and the private-network guard.
func (m *UpstreamManager) Config(row *model.Upstream) (upstream.Config, error) {
	open := func(col, purpose, sealed string) (string, error) {
		if sealed == "" {
			return "", nil
		}
		b, err := m.ring.Open(purpose, crypto.AAD{Table: "upstreams", RowID: row.PublicID, Column: col}, sealed)
		return string(b), err
	}
	pass, err := open("password_sealed", crypto.PurposeUpstreamSecret, row.PasswordSealed)
	if err != nil {
		return upstream.Config{}, err
	}
	smtpPass, err := open("smtp_password_sealed", crypto.PurposeUpstreamSecret, row.SMTPPasswordSealed)
	if err != nil {
		return upstream.Config{}, err
	}
	if smtpPass == "" {
		smtpPass = pass
	}
	proxy, err := m.proxyFor(row)
	if err != nil {
		return upstream.Config{}, err
	}
	smtpUser := row.SMTPUsername
	if smtpUser == "" {
		smtpUser = row.IMAPUsername
	}
	return upstream.Config{
		AccountID: row.PublicID,
		Kind:      row.Kind,
		Email:     row.Email,
		IMAP:      upstream.Endpoint{Host: row.IMAPHost, Port: row.IMAPPort, TLS: row.IMAPTLS, Username: row.IMAPUsername, Password: pass},
		SMTP:      upstream.Endpoint{Host: row.SMTPHost, Port: row.SMTPPort, TLS: row.SMTPTLS, Username: smtpUser, Password: smtpPass},
		Dialer:    netx.New(netx.Options{Proxy: proxy, AllowPrivate: m.settings.Bool(SettingAllowPrivateNetworks)}),
		Log:       m.log,
	}, nil
}

// proxyFor resolves an upstream's proxy mode to a proxy, or nil for direct.
func (m *UpstreamManager) proxyFor(row *model.Upstream) (*netx.Proxy, error) {
	switch row.ProxyMode {
	case model.ProxyNone:
		return nil, nil
	case model.ProxyCustom:
		p, err := netx.ParseProxyURL(row.ProxyURL)
		if err != nil {
			return nil, err
		}
		if row.ProxyPasswordSealed != "" {
			b, err := m.ring.Open(crypto.PurposeProxySecret, crypto.AAD{Table: "upstreams", RowID: row.PublicID, Column: "proxy_password_sealed"}, row.ProxyPasswordSealed)
			if err != nil {
				return nil, err
			}
			p.Password = string(b)
		}
		return p, nil
	}
	def := m.settings.String(SettingProxyDefault)
	if def == "" {
		return nil, nil
	}
	p, err := netx.ParseProxyURL(def)
	if err != nil {
		return nil, err
	}
	if p.Password, err = m.settings.Secret(SettingProxyDefaultPassword); err != nil {
		return nil, err
	}
	return p, nil
}

func (m *UpstreamManager) userPublicID(id uint) (string, error) {
	var u model.User
	if err := m.db.Select("public_id").First(&u, id).Error; err != nil {
		return "", err
	}
	return u.PublicID, nil
}

// record stores a status and ends the user's streams when it changed.
func (m *UpstreamManager) record(accountID, userID string, st upstream.Status) {
	m.mu.Lock()
	prev, known := m.statuses[accountID]
	m.statuses[accountID] = st.State
	m.mu.Unlock()
	err := m.db.Model(&model.Upstream{}).Where("public_id = ?", accountID).Updates(map[string]any{
		"status": st.State, "status_code": st.Code, "status_since": st.Since, "last_error": st.Error,
	}).Error
	if err != nil {
		m.log.Error("save upstream status", "account", accountID, "err", err)
	}
	if !known || prev != st.State {
		m.endStreams(userID)
	}
}

func (m *UpstreamManager) endStreams(userID string) {
	m.mu.Lock()
	n := m.notifier
	m.mu.Unlock()
	if n != nil {
		n.EndUserStreams(userID)
	}
}

func (m *UpstreamManager) saveCaps(accountID string, caps *upstream.Capabilities) {
	b, _ := json.Marshal(caps)
	m.db.Model(&model.Upstream{}).Where("public_id = ?", accountID).Update("server_caps", string(b))
}

// runnerEvents connects one runner to the manager.
type runnerEvents struct {
	m      *UpstreamManager
	userID string
	once   sync.Once
}

func (e *runnerEvents) StatusChanged(accountID string, st upstream.Status) {
	e.m.log.Info("upstream status", "account", accountID, "status", st.State, "code", st.Code)
	e.m.record(accountID, e.userID, st)
	if st.State == jmapserver.UpstreamOK {
		e.once.Do(func() {
			go func() {
				e.m.mu.Lock()
				r := e.m.running[accountID]
				e.m.mu.Unlock()
				if r == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if caps, err := r.up.Probe(ctx); err == nil {
					e.m.saveCaps(accountID, caps)
				}
			}()
		})
	}
}

func (e *runnerEvents) Progress(string, int, int) {}

func (e *runnerEvents) Changed(accountID string, states map[string]string) {
	e.m.mu.Lock()
	n := e.m.notifier
	e.m.mu.Unlock()
	if n != nil {
		n.Publish(e.userID, jmapserver.Changes{accountID: states})
	}
}
