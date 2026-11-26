package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

const pushTable = "push_subscriptions"

func init() {
	for _, col := range []string{"url", "auth"} {
		RegisterSealedColumn(SealedColumn{Table: pushTable, IDColumn: "public_id", Column: col, Purpose: crypto.PurposePushKey})
	}
}

// PushStore keeps JMAP push subscriptions in gateway.sqlite (jmapserver.PushStore).
type PushStore struct {
	db   *gorm.DB
	ring *crypto.Keyring
	log  *slog.Logger
}

// NewPushStore returns the store; ring seals the url and auth columns.
func NewPushStore(db *gorm.DB, ring *crypto.Keyring, log *slog.Logger) *PushStore {
	return &PushStore{db: db, ring: ring, log: log}
}

func pushAAD(publicID, column string) crypto.AAD {
	return crypto.AAD{Table: pushTable, RowID: publicID, Column: column}
}

// ListPush implements jmapserver.PushStore.
func (s *PushStore) ListPush(ctx context.Context, userID string) ([]*jmapserver.PushSubscription, error) {
	var rows []model.PushSubscription
	if err := s.db.WithContext(ctx).Where("user_public_id = ?", userID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*jmapserver.PushSubscription, 0, len(rows))
	for i := range rows {
		sub, err := s.decode(&rows[i])
		if err != nil {
			// One unreadable row must not lock the user out of push; it is pruned when it expires.
			s.log.Error("push: skip unreadable subscription", "subscription", rows[i].PublicID, "err", err)
			continue
		}
		out = append(out, sub)
	}
	return out, nil
}

// CreatePush implements jmapserver.PushStore.
func (s *PushStore) CreatePush(ctx context.Context, sub *jmapserver.PushSubscription) error {
	row, err := s.encode(sub)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(row).Error
}

// UpdatePush implements jmapserver.PushStore; url and keys never change.
func (s *PushStore) UpdatePush(ctx context.Context, sub *jmapserver.PushSubscription) error {
	types, _ := json.Marshal(sub.Types)
	emailPush, _ := json.Marshal(sub.EmailPush)
	return s.db.WithContext(ctx).Model(&model.PushSubscription{}).
		Where("public_id = ? AND user_public_id = ?", sub.ID, sub.UserID).
		Updates(map[string]any{
			"verification_code": sub.VerificationCode,
			"verified":          sub.Verified,
			"expires":           sub.Expires.UTC(),
			"types":             string(types),
			"email_push":        string(emailPush),
			"updated_at":        time.Now().UTC(),
		}).Error
}

// DeletePush implements jmapserver.PushStore.
func (s *PushStore) DeletePush(ctx context.Context, userID string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Where("user_public_id = ? AND public_id IN ?", userID, ids).
		Delete(&model.PushSubscription{}).Error
}

// PushUsers implements jmapserver.PushStore.
func (s *PushStore) PushUsers(ctx context.Context) ([]string, error) {
	var users []string
	err := s.db.WithContext(ctx).Model(&model.PushSubscription{}).Distinct("user_public_id").Pluck("user_public_id", &users).Error
	return users, err
}

// PruneExpired deletes subscriptions that expired before now; clients renew
// theirs long before (czlmail every 12 hours).
func (s *PushStore) PruneExpired(now time.Time) (int64, error) {
	res := s.db.Where("expires < ?", now.UTC()).Delete(&model.PushSubscription{})
	return res.RowsAffected, res.Error
}

func (s *PushStore) encode(sub *jmapserver.PushSubscription) (*model.PushSubscription, error) {
	url, err := s.ring.Seal(crypto.PurposePushKey, pushAAD(sub.ID, "url"), []byte(sub.URL))
	if err != nil {
		return nil, err
	}
	row := &model.PushSubscription{
		PublicID: sub.ID, UserPublicID: sub.UserID, CredentialID: sub.CredentialID,
		DeviceClientID: sub.DeviceClientID, URL: url,
		VerificationCode: sub.VerificationCode, Verified: sub.Verified, Expires: sub.Expires.UTC(),
		CreatedAt: sub.CreatedAt,
	}
	if sub.P256DH != nil {
		row.P256DH = base64.RawURLEncoding.EncodeToString(sub.P256DH)
		if row.Auth, err = s.ring.Seal(crypto.PurposePushKey, pushAAD(sub.ID, "auth"), sub.Auth); err != nil {
			return nil, err
		}
	}
	types, _ := json.Marshal(sub.Types)
	emailPush, _ := json.Marshal(sub.EmailPush)
	row.Types, row.EmailPush = string(types), string(emailPush)
	return row, nil
}

func (s *PushStore) decode(row *model.PushSubscription) (*jmapserver.PushSubscription, error) {
	url, err := s.ring.Open(crypto.PurposePushKey, pushAAD(row.PublicID, "url"), row.URL)
	if err != nil {
		return nil, err
	}
	sub := &jmapserver.PushSubscription{
		ID: row.PublicID, UserID: row.UserPublicID, CredentialID: row.CredentialID,
		DeviceClientID: row.DeviceClientID, URL: string(url),
		VerificationCode: row.VerificationCode, Verified: row.Verified, Expires: row.Expires.UTC(),
		CreatedAt: row.CreatedAt,
	}
	if row.P256DH != "" {
		if sub.P256DH, err = base64.RawURLEncoding.DecodeString(row.P256DH); err != nil {
			return nil, err
		}
		if sub.Auth, err = s.ring.Open(crypto.PurposePushKey, pushAAD(row.PublicID, "auth"), row.Auth); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal([]byte(row.Types), &sub.Types); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(row.EmailPush), &sub.EmailPush); err != nil {
		return nil, err
	}
	return sub, nil
}
