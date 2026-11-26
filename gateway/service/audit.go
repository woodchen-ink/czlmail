package service

import (
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// AuditRetention is how long admin write records are kept.
const AuditRetention = 180 * 24 * time.Hour

// Audit stores and lists the admin write log.
type Audit struct {
	db  *gorm.DB
	now func() time.Time
}

// NewAudit returns the audit log service.
func NewAudit(db *gorm.DB) *Audit { return &Audit{db: db, now: time.Now} }

// Record stores one entry; Params must already be masked (see MaskParams).
func (a *Audit) Record(e *model.AuditLog) error {
	e.PublicID = NewPublicID()
	return a.db.Create(e).Error
}

// List returns up to limit entries older than the cursor (an entry's public id, or ""
// for the newest), newest first, and the cursor of the next page ("" at the end).
func (a *Audit) List(actor *model.User, cursor string, limit int) ([]model.AuditLog, string, error) {
	if !IsAdmin(actor) {
		return nil, "", ErrForbidden
	}
	q := a.db.Order("public_id DESC").Limit(limit + 1)
	if cursor != "" {
		q = q.Where("public_id < ?", cursor)
	}
	var rows []model.AuditLog
	if err := q.Find(&rows).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].PublicID
	}
	return rows, next, nil
}

// Prune deletes entries older than AuditRetention.
func (a *Audit) Prune() (int64, error) {
	res := a.db.Where("created_at < ?", a.now().UTC().Add(-AuditRetention)).Delete(&model.AuditLog{})
	return res.RowsAffected, res.Error
}
