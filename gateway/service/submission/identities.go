package submission

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Identity errors.
var (
	ErrIdentityNotFound   = errors.New("identity not found")
	ErrIdentityManaged    = errors.New("identity is managed by the upstream")
	ErrInvalidIdentity    = errors.New("invalid identity address")
	ErrIdentityNotAllowed = errors.New("address not covered by an identity")
	ErrAccountNotFound    = errors.New("account not found")
)

// Address is a JMAP EmailAddress.
type Address struct {
	Name  *string `json:"name"`
	Email string  `json:"email"`
}

// IdentityInput creates or changes an identity. Nil fields are left as they are.
type IdentityInput struct {
	Email         *string
	Name          *string
	ReplyTo       *[]Address // empty slice clears
	Bcc           *[]Address
	TextSignature *string
	HTMLSignature *string
}

// Identities stores the identities of upstream accounts in gateway.sqlite.
type Identities struct {
	db     *gorm.DB
	log    changeLog
	logger *slog.Logger
	// source returns the account's upstream when it lists identities itself.
	source func(accountID string) upstream.IdentitySource
	// changed is told the accounts whose Identity state moved (push).
	changed func(accountID string)
}

func newIdentities(db *gorm.DB, logger *slog.Logger, source func(string) upstream.IdentitySource, changed func(string)) *Identities {
	return &Identities{db: db, log: changeLog{db: db}, logger: logger, source: source, changed: changed}
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns prefix + 20 random base32 characters (100 bits).
func newID(prefix string) string {
	var b [13]byte
	_, _ = rand.Read(b[:])
	return prefix + idEncoding.EncodeToString(b[:])[:20]
}

func (s *Identities) upstream(accountID string) (*model.Upstream, error) {
	var up model.Upstream
	err := s.db.Where("public_id = ?", accountID).First(&up).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}
	return &up, err
}

// List returns the account's identities, primary first, creating the primary
// identity on first use and following changes of the upstream's address.
func (s *Identities) List(accountID string) ([]model.Identity, error) {
	up, err := s.upstream(accountID)
	if err != nil {
		return nil, err
	}
	if src := s.source(accountID); src != nil {
		// The upstream knows its own identities (its own address among them).
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		list, err := src.Identities(ctx)
		cancel()
		if err == nil {
			err = s.syncSource(accountID, list)
		}
		if err != nil {
			s.logger.Warn("upstream identities", "account", accountID, "err", err) // keep the stored ones
		}
	} else if err := s.ensurePrimary(up); err != nil {
		return nil, err
	}
	var rows []model.Identity
	err = s.db.Where("account_id = ?", accountID).
		Order("CASE source WHEN 'primary' THEN 0 WHEN 'provider' THEN 1 ELSE 2 END, id").Find(&rows).Error
	return rows, err
}

func (s *Identities) ensurePrimary(up *model.Upstream) error {
	var p model.Identity
	err := s.db.Where("account_id = ? AND source = ?", up.PublicID, model.IdentityPrimary).First(&p).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		p = model.Identity{PublicID: newID("i"), AccountID: up.PublicID, Source: model.IdentityPrimary, Email: strings.ToLower(up.Email)}
		return s.write(up.PublicID, func(tx *gorm.DB) error {
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
			return s.log.record(tx, up.PublicID, TypeIdentity, p.PublicID, kindCreated)
		})
	case err != nil:
		return err
	case !strings.EqualFold(p.Email, up.Email):
		return s.write(up.PublicID, func(tx *gorm.DB) error {
			if err := tx.Model(&p).Update("email", strings.ToLower(up.Email)).Error; err != nil {
				return err
			}
			return s.log.record(tx, up.PublicID, TypeIdentity, p.PublicID, kindUpdated)
		})
	}
	return nil
}

func (s *Identities) write(accountID string, fn func(tx *gorm.DB) error) error {
	if err := s.db.Transaction(fn); err != nil {
		return err
	}
	if s.changed != nil {
		s.changed(accountID)
	}
	return nil
}

// Get returns one identity of the account.
func (s *Identities) Get(accountID, id string) (*model.Identity, error) {
	var row model.Identity
	err := s.db.Where("account_id = ? AND public_id = ?", accountID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIdentityNotFound
	}
	return &row, err
}

// State is the account's Identity state.
func (s *Identities) State(accountID string) (string, error) {
	return s.log.state(s.db, accountID, TypeIdentity)
}

// normalizeAddress checks an identity address: a mailbox, or "*@domain".
func normalizeAddress(addr string) (string, error) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(domain, "@ <>\"") || len(addr) > 254 {
		return "", ErrInvalidIdentity
	}
	if local == "*" {
		if _, err := mail.ParseAddress("x@" + domain); err != nil {
			return "", ErrInvalidIdentity
		}
		return addr, nil
	}
	a, err := mail.ParseAddress(addr)
	if err != nil || !strings.EqualFold(a.Address, addr) {
		return "", ErrInvalidIdentity
	}
	return addr, nil
}

// Create registers an alias. adminOK lets the admin add any address; through
// JMAP (Identity/set) the address must already be covered by an identity.
func (s *Identities) Create(accountID string, in IdentityInput, adminOK bool) (*model.Identity, error) {
	if in.Email == nil {
		return nil, ErrInvalidIdentity
	}
	addr, err := normalizeAddress(*in.Email)
	if err != nil {
		return nil, err
	}
	list, err := s.List(accountID)
	if err != nil {
		return nil, err
	}
	if !adminOK && !covered(list, addr) {
		return nil, ErrIdentityNotAllowed
	}
	row := model.Identity{PublicID: newID("i"), AccountID: accountID, Source: model.IdentityAlias, Email: addr}
	if err := apply(&row, in); err != nil {
		return nil, err
	}
	err = s.write(accountID, func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return s.log.record(tx, accountID, TypeIdentity, row.PublicID, kindCreated)
	})
	return &row, err
}

// Update changes the name, reply-to, bcc and signatures of an identity; the
// address of an alias may change too.
func (s *Identities) Update(accountID, id string, in IdentityInput) (*model.Identity, error) {
	row, err := s.Get(accountID, id)
	if err != nil {
		return nil, err
	}
	if in.Email != nil {
		addr, err := normalizeAddress(*in.Email)
		if err != nil {
			return nil, err
		}
		if addr != row.Email {
			if row.Source != model.IdentityAlias {
				return nil, ErrIdentityManaged
			}
			row.Email = addr
		}
	}
	if err := apply(row, in); err != nil {
		return nil, err
	}
	err = s.write(accountID, func(tx *gorm.DB) error {
		if err := tx.Save(row).Error; err != nil {
			return err
		}
		return s.log.record(tx, accountID, TypeIdentity, row.PublicID, kindUpdated)
	})
	return row, err
}

// Delete removes an alias; the primary and provider identities cannot be deleted.
func (s *Identities) Delete(accountID, id string) error {
	row, err := s.Get(accountID, id)
	if err != nil {
		return err
	}
	if row.Source != model.IdentityAlias {
		return ErrIdentityManaged
	}
	return s.write(accountID, func(tx *gorm.DB) error {
		if err := tx.Delete(row).Error; err != nil {
			return err
		}
		return s.log.record(tx, accountID, TypeIdentity, row.PublicID, kindDestroyed)
	})
}

// syncSource mirrors the identities an upstream lists itself (Gmail send-as,
// upstream.IdentitySource) into provider rows that keep the upstream's ids. The
// provider's name, Reply-To and signature fill a new row; afterwards the
// gateway's stored values win, so Identity/set edits stay.
func (s *Identities) syncSource(accountID string, list []upstream.Identity) error {
	var have []model.Identity
	if err := s.db.Where("account_id = ? AND source = ?", accountID, model.IdentityProvider).Find(&have).Error; err != nil {
		return err
	}
	// Rows match by the upstream's key within this account (upsert): the same
	// address can be connected by several accounts, and a provider may list an
	// address twice (differing in case).
	byID := map[string]*model.Identity{}
	for i := range have {
		key := have[i].ProviderKey
		if key == "" {
			key = have[i].PublicID
		}
		byID[key] = &have[i]
	}
	type change struct {
		row  *model.Identity
		kind string
	}
	var changes []change
	seen := map[string]bool{}
	for _, p := range list {
		addr, err := normalizeAddress(p.Email)
		if err != nil || p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		row := byID[p.ID]
		switch {
		case row == nil:
			row = &model.Identity{PublicID: providerIdentityID(accountID, p.ID), AccountID: accountID, Source: model.IdentityProvider, ProviderKey: p.ID, Email: addr}
			in := IdentityInput{Name: &p.Name, HTMLSignature: &p.HTMLSignature}
			if p.ReplyTo != "" {
				in.ReplyTo = &[]Address{{Email: p.ReplyTo}}
			}
			if err := apply(row, in); err != nil {
				continue
			}
			changes = append(changes, change{row, kindCreated})
		case row.Email != addr:
			row.Email = addr
			changes = append(changes, change{row, kindUpdated})
		}
	}
	for id, row := range byID {
		if !seen[id] {
			changes = append(changes, change{row, kindDestroyed})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return s.write(accountID, func(tx *gorm.DB) error {
		for _, c := range changes {
			var err error
			if c.kind == kindDestroyed {
				err = tx.Delete(c.row).Error
			} else {
				err = tx.Save(c.row).Error
			}
			if err != nil {
				return err
			}
			if err := s.log.record(tx, accountID, TypeIdentity, c.row.PublicID, c.kind); err != nil {
				return err
			}
		}
		return nil
	})
}

// providerIdentityID is the JMAP id of a provider identity: stable for the
// (account, provider key) pair and unique across accounts, since Identity ids
// are unique in the gateway database while the provider's key (Gmail: derived
// from the address) is not.
func providerIdentityID(accountID, key string) string {
	sum := sha256.Sum256([]byte(accountID + "/" + key))
	return "i" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))[:20]
}

const maxSignature = 64 << 10

func apply(row *model.Identity, in IdentityInput) error {
	if in.Name != nil {
		if len(*in.Name) > 256 {
			return ErrInvalidIdentity
		}
		row.Name = *in.Name
	}
	enc := func(list *[]Address) (string, error) {
		if len(*list) == 0 {
			return "", nil
		}
		for _, a := range *list {
			if _, err := mail.ParseAddress(a.Email); err != nil {
				return "", ErrInvalidIdentity
			}
		}
		b, err := json.Marshal(*list)
		return string(b), err
	}
	if in.ReplyTo != nil {
		v, err := enc(in.ReplyTo)
		if err != nil {
			return err
		}
		row.ReplyTo = v
	}
	if in.Bcc != nil {
		v, err := enc(in.Bcc)
		if err != nil {
			return err
		}
		row.Bcc = v
	}
	if in.TextSignature != nil {
		if len(*in.TextSignature) > maxSignature {
			return ErrInvalidIdentity
		}
		row.TextSignature = *in.TextSignature
	}
	if in.HTMLSignature != nil {
		if len(*in.HTMLSignature) > maxSignature {
			return ErrInvalidIdentity
		}
		row.HTMLSignature = *in.HTMLSignature
	}
	return nil
}

// matches reports whether addr is allowed by an identity address (exact, or
// any local part for "*@domain").
func matches(identity, addr string) bool {
	identity, addr = strings.ToLower(identity), strings.ToLower(strings.TrimSpace(addr))
	if domain, ok := strings.CutPrefix(identity, "*@"); ok {
		_, d, found := strings.Cut(addr, "@")
		return found && d == domain
	}
	return identity == addr
}

func covered(list []model.Identity, addr string) bool {
	for _, i := range list {
		if matches(i.Email, addr) {
			return true
		}
	}
	return false
}

// DecodeAddresses reads a stored address list (empty: none).
func DecodeAddresses(s string) []Address {
	if s == "" {
		return nil
	}
	var out []Address
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
