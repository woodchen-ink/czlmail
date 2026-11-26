package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Upstream errors.
var (
	ErrUpstreamNotFound     = errors.New("upstream not found")
	ErrPrivateNetwork       = errors.New("host is in a private network")
	ErrUpstreamKindReadOnly = errors.New("this upstream kind is set up by its own flow")
	// ErrPasswordRequired: the server, port, TLS mode or username changed, so the
	// stored password is not sent anywhere new without being entered again.
	ErrPasswordRequired = errors.New("password required for the changed server")
)

// Sealed columns of the upstreams table.
func init() {
	for _, c := range []struct{ col, purpose string }{
		{"password_sealed", crypto.PurposeUpstreamSecret},
		{"smtp_password_sealed", crypto.PurposeUpstreamSecret},
		{"oauth_token_sealed", crypto.PurposeUpstreamSecret},
		{"proxy_password_sealed", crypto.PurposeProxySecret},
	} {
		RegisterSealedColumn(SealedColumn{Table: "upstreams", IDColumn: "public_id", Column: c.col, Purpose: c.purpose})
	}
}

// Endpoint is a server as the admin enters it. A nil Password keeps the stored one.
type Endpoint struct {
	Host     string
	Port     int
	TLS      string
	Username string
	Password *string
}

// ProxyInput is an upstream's proxy setting. A nil Password keeps the stored one.
type ProxyInput struct {
	Mode     string // model.ProxyFollow, ProxyNone, ProxyCustom
	URL      string // custom only
	Password *string
}

// UpstreamInput creates or replaces an IMAP upstream's settings. On update, nil
// sections are left as they are.
type UpstreamInput struct {
	Name  string
	Email string
	IMAP  *Endpoint
	SMTP  *Endpoint
	Proxy *ProxyInput
}

// Upstreams manages the upstream rows of gateway.sqlite. Running them is the
// UpstreamManager's job; every change that affects a connection restarts it.
type Upstreams struct {
	db       *gorm.DB
	ring     *crypto.Keyring
	settings *Settings
	mgr      *UpstreamManager
	log      *slog.Logger
	now      func() time.Time
}

// NewUpstreams returns the upstream service.
func NewUpstreams(db *gorm.DB, ring *crypto.Keyring, settings *Settings, mgr *UpstreamManager, log *slog.Logger) *Upstreams {
	return &Upstreams{db: db, ring: ring, settings: settings, mgr: mgr, log: log, now: time.Now}
}

// owner resolves whose upstreams the actor is working with: themselves, or (for
// admins) the user named by ownerPublicID.
func (s *Upstreams) owner(actor *model.User, ownerPublicID string) (*model.User, error) {
	if ownerPublicID == "" || ownerPublicID == actor.PublicID {
		return actor, nil
	}
	var u model.User
	if err := s.db.Where("public_id = ?", ownerPublicID).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if err := canManage(actor, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// find loads an upstream the actor may manage.
func (s *Upstreams) find(actor *model.User, publicID string) (*model.Upstream, error) {
	var up model.Upstream
	err := s.db.Where("public_id = ?", publicID).First(&up).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUpstreamNotFound
	}
	if err != nil {
		return nil, err
	}
	if up.UserID == actor.ID {
		return &up, nil
	}
	var owner model.User
	if err := s.db.First(&owner, up.UserID).Error; err != nil {
		return nil, ErrUpstreamNotFound
	}
	if canManage(actor, &owner) != nil {
		return nil, ErrUpstreamNotFound // do not reveal other users' upstreams
	}
	return &up, nil
}

// UpstreamDetail is an upstream with its live status, for the admin.
type UpstreamDetail struct {
	model.Upstream
	OwnerPublicID string
	Live          upstream.Status
	Capabilities  *upstream.Capabilities
}

// List returns the upstreams of the actor (or of ownerPublicID, for admins).
func (s *Upstreams) List(actor *model.User, ownerPublicID string) ([]UpstreamDetail, error) {
	owner, err := s.owner(actor, ownerPublicID)
	if err != nil {
		return nil, err
	}
	var rows []model.Upstream
	if err := s.db.Where("user_id = ?", owner.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]UpstreamDetail, len(rows))
	for i := range rows {
		out[i] = s.detail(&rows[i], owner.PublicID)
	}
	return out, nil
}

// Get returns one upstream.
func (s *Upstreams) Get(actor *model.User, publicID string) (*UpstreamDetail, error) {
	up, err := s.find(actor, publicID)
	if err != nil {
		return nil, err
	}
	var owner model.User
	if err := s.db.Select("public_id").First(&owner, up.UserID).Error; err != nil {
		return nil, err
	}
	d := s.detail(up, owner.PublicID)
	return &d, nil
}

func (s *Upstreams) detail(up *model.Upstream, ownerPublicID string) UpstreamDetail {
	d := UpstreamDetail{Upstream: *up, OwnerPublicID: ownerPublicID, Live: s.mgr.Status(up)}
	if up.ServerCaps != "" {
		var c upstream.Capabilities
		if json.Unmarshal([]byte(up.ServerCaps), &c) == nil {
			d.Capabilities = &c
		}
	}
	return d
}

// CreateIMAP adds an IMAP + SMTP upstream for the owner and starts syncing it.
// The first upstream of a user becomes the default.
func (s *Upstreams) CreateIMAP(ctx context.Context, actor *model.User, ownerPublicID string, in UpstreamInput) (*UpstreamDetail, error) {
	owner, err := s.owner(actor, ownerPublicID)
	if err != nil {
		return nil, err
	}
	up := &model.Upstream{
		PublicID: NewAccountID(), UserID: owner.ID, Kind: model.UpstreamKindIMAP, Name: in.Name, Email: in.Email,
		ProxyMode: model.ProxyFollow, Status: jmapserver.UpstreamSyncing, StatusSince: s.now().UTC(),
	}
	if err := s.apply(up, in); err != nil {
		return nil, err
	}
	if err := s.checkHosts(ctx, up); err != nil {
		return nil, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.Upstream{}).Where("user_id = ?", owner.ID).Count(&n).Error; err != nil {
			return err
		}
		up.IsDefault = n == 0
		return tx.Create(up).Error
	})
	if err != nil {
		return nil, err
	}
	s.mgr.Start(up)
	d := s.detail(up, owner.PublicID)
	return &d, nil
}

// Update changes an upstream's settings. Anything that affects the connection
// restarts it (an authRequired upstream retries with the new password).
func (s *Upstreams) Update(ctx context.Context, actor *model.User, publicID string, in UpstreamInput) (*UpstreamDetail, error) {
	up, err := s.find(actor, publicID)
	if err != nil {
		return nil, err
	}
	if up.Kind != model.UpstreamKindIMAP && (in.IMAP != nil || in.SMTP != nil) {
		return nil, ErrUpstreamKindReadOnly
	}
	if err := s.apply(up, in); err != nil {
		return nil, err
	}
	if err := s.checkHosts(ctx, up); err != nil {
		return nil, err
	}
	if err := s.db.Save(up).Error; err != nil {
		return nil, err
	}
	if in.IMAP != nil || in.SMTP != nil || in.Proxy != nil {
		s.mgr.Restart(up)
	} else {
		s.mgr.Renamed(up)
	}
	return s.Get(actor, publicID)
}

// apply copies validated input onto the row, sealing secrets.
func (s *Upstreams) apply(up *model.Upstream, in UpstreamInput) error {
	if in.Name != "" {
		up.Name = in.Name
	}
	if in.Email != "" {
		up.Email = in.Email
	}
	seal := func(col, purpose string, plain *string, dst *string) error {
		if plain == nil {
			return nil
		}
		if *plain == "" {
			*dst = ""
			return nil
		}
		v, err := s.ring.Seal(purpose, crypto.AAD{Table: "upstreams", RowID: up.PublicID, Column: col}, []byte(*plain))
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	hadIMAP := up.PasswordSealed != ""
	hadSMTP := up.SMTPPasswordSealed != "" || hadIMAP
	if e := in.IMAP; e != nil {
		if e.Username == "" {
			e.Username = up.Email
		}
		moved := e.Host != up.IMAPHost || e.Port != up.IMAPPort || e.TLS != up.IMAPTLS || e.Username != up.IMAPUsername
		if moved && hadIMAP && e.Password == nil {
			return ErrPasswordRequired
		}
		up.IMAPHost, up.IMAPPort, up.IMAPTLS, up.IMAPUsername = e.Host, e.Port, e.TLS, e.Username
		if err := seal("password_sealed", crypto.PurposeUpstreamSecret, e.Password, &up.PasswordSealed); err != nil {
			return err
		}
	}
	if e := in.SMTP; e != nil {
		moved := e.Host != up.SMTPHost || e.Port != up.SMTPPort || e.TLS != up.SMTPTLS || e.Username != up.SMTPUsername
		if moved && hadSMTP && up.SMTPHost != "" && e.Password == nil {
			return ErrPasswordRequired
		}
		up.SMTPHost, up.SMTPPort, up.SMTPTLS, up.SMTPUsername = e.Host, e.Port, e.TLS, e.Username
		if err := seal("smtp_password_sealed", crypto.PurposeUpstreamSecret, e.Password, &up.SMTPPasswordSealed); err != nil {
			return err
		}
	}
	if p := in.Proxy; p != nil {
		oldProxyURL := up.ProxyURL
		up.ProxyMode = p.Mode
		up.ProxyURL = ""
		if p.Mode == model.ProxyCustom {
			if p.URL != oldProxyURL && p.Password == nil {
				up.ProxyPasswordSealed = "" // never send the old proxy password to a new proxy
			}
			up.ProxyURL = p.URL
			if err := seal("proxy_password_sealed", crypto.PurposeProxySecret, p.Password, &up.ProxyPasswordSealed); err != nil {
				return err
			}
		} else {
			up.ProxyPasswordSealed = ""
		}
	}
	return nil
}

// checkHosts refuses private-network hosts up front (the dialer refuses them at
// connection time too, this only reports it at once): the mail servers when
// connecting directly, the proxy when one is used.
func (s *Upstreams) checkHosts(ctx context.Context, up *model.Upstream) error {
	if s.settings.Bool(SettingAllowPrivateNetworks) {
		return nil
	}
	proxy, err := s.proxyFor(up)
	if err != nil {
		return err
	}
	hosts := []string{up.IMAPHost, up.SMTPHost}
	if proxy != nil {
		hosts = []string{proxy.Host} // the proxy resolves the mail servers
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if err := netx.CheckHost(ctx, h); err != nil {
			if errors.Is(err, netx.ErrPrivateAddress) {
				return ErrPrivateNetwork
			}
			// An unresolvable name is reported by the connection test, not here.
		}
	}
	return nil
}

// SetPaused pauses or resumes an upstream.
func (s *Upstreams) SetPaused(actor *model.User, publicID string, paused bool) (*UpstreamDetail, error) {
	up, err := s.find(actor, publicID)
	if err != nil {
		return nil, err
	}
	if up.Paused != paused {
		up.Paused = paused
		if err := s.db.Model(up).Update("paused", paused).Error; err != nil {
			return nil, err
		}
		s.mgr.Restart(up)
	}
	return s.Get(actor, publicID)
}

// SetDefault makes an upstream its owner's default (primary) account.
func (s *Upstreams) SetDefault(actor *model.User, publicID string) (*UpstreamDetail, error) {
	up, err := s.find(actor, publicID)
	if err != nil {
		return nil, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Upstream{}).Where("user_id = ? AND id <> ?", up.UserID, up.ID).Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(up).Update("is_default", true).Error
	})
	if err != nil {
		return nil, err
	}
	s.mgr.Renamed(up)
	return s.Get(actor, publicID)
}

// Delete removes an upstream: its runner stops, its index file is deleted and
// its account leaves the Session. Another upstream becomes the default if needed.
func (s *Upstreams) Delete(actor *model.User, publicID string) error {
	up, err := s.find(actor, publicID)
	if err != nil {
		return err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(up).Error; err != nil {
			return err
		}
		if !up.IsDefault {
			return nil
		}
		var next model.Upstream
		err := tx.Where("user_id = ?", up.UserID).Order("id").First(&next).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return tx.Model(&next).Update("is_default", true).Error
	})
	if err != nil {
		return err
	}
	s.mgr.Remove(up)
	return nil
}

// Test checks connection settings that are not saved yet. existingID, when set,
// supplies stored passwords the input leaves out.
func (s *Upstreams) Test(ctx context.Context, actor *model.User, existingID string, in UpstreamInput) (*upstream.ProbeReport, error) {
	up := &model.Upstream{PublicID: "probe", Kind: model.UpstreamKindIMAP, ProxyMode: model.ProxyFollow}
	if existingID != "" {
		stored, err := s.find(actor, existingID)
		if err != nil {
			return nil, err
		}
		up = stored
	}
	// Seal against the row being tested so stored and new secrets mix.
	if err := s.apply(up, in); err != nil {
		return nil, err
	}
	if err := s.checkHosts(ctx, up); err != nil {
		return nil, err
	}
	cfg, err := s.mgr.Config(up)
	if err != nil {
		return nil, err
	}
	rep, err := upstream.Probe(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if existingID != "" && rep.IMAP.Capabilities != nil {
		b, _ := json.Marshal(rep.IMAP.Capabilities)
		s.db.Model(&model.Upstream{}).Where("id = ?", up.ID).Update("server_caps", string(b))
	}
	return rep, nil
}

// TestStored checks a saved upstream as it is.
func (s *Upstreams) TestStored(ctx context.Context, actor *model.User, publicID string) (*upstream.ProbeReport, error) {
	return s.Test(ctx, actor, publicID, UpstreamInput{})
}

// proxyFor returns the proxy an upstream uses, or nil for direct connections.
func (s *Upstreams) proxyFor(up *model.Upstream) (*netx.Proxy, error) {
	return s.mgr.proxyFor(up)
}

// NewAccountID returns a random JMAP accountId: "a" + 20 base32 characters (100 bits).
func NewAccountID() string { return "a" + randomB32(20) }
