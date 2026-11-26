package submission

import (
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Types with a gateway-kept changelog.
const (
	TypeEmailSubmission = "EmailSubmission"
	TypeIdentity        = "Identity"
)

// Change kinds.
const (
	kindCreated   = "created"
	kindUpdated   = "updated"
	kindDestroyed = "destroyed"
)

var errCannotCalculate = errors.New("cannot calculate changes")

// changeLog keeps object_changes; a state is the account's last seq of the type.
type changeLog struct{ db *gorm.DB }

func (l changeLog) record(tx *gorm.DB, accountID, typ, id, kind string) error {
	return tx.Create(&model.ObjectChange{AccountID: accountID, Type: typ, ObjectID: id, Kind: kind, CreatedAt: time.Now().UTC()}).Error
}

func (l changeLog) state(db *gorm.DB, accountID, typ string) (string, error) {
	var seq int64
	err := db.Model(&model.ObjectChange{}).Where("account_id = ? AND type = ?", accountID, typ).
		Select("COALESCE(MAX(seq), 0)").Scan(&seq).Error
	if err != nil {
		return "", err
	}
	if seq == 0 {
		var f model.ObjectChangeFloor
		if db.Where("account_id = ? AND type = ?", accountID, typ).First(&f).Error == nil {
			seq = f.MinSeq
		}
	}
	return strconv.FormatInt(seq, 10), nil
}

// changeSet is the answer to /changes.
type changeSet struct {
	Created, Updated, Destroyed []string
	NewState                    string
	HasMore                     bool
}

func (l changeLog) changes(accountID, typ, since string, max int) (*changeSet, error) {
	from, err := strconv.ParseInt(since, 10, 64)
	if err != nil || from < 0 {
		return nil, errCannotCalculate
	}
	var floor model.ObjectChangeFloor
	if l.db.Where("account_id = ? AND type = ?", accountID, typ).First(&floor).Error == nil && from < floor.MinSeq {
		return nil, errCannotCalculate
	}
	cur, err := l.state(l.db, accountID, typ)
	if err != nil {
		return nil, err
	}
	if n, _ := strconv.ParseInt(cur, 10, 64); from > n {
		return nil, errCannotCalculate
	}
	var rows []model.ObjectChange
	q := l.db.Where("account_id = ? AND type = ? AND seq > ?", accountID, typ, from).Order("seq")
	if max > 0 {
		q = q.Limit(max + 1)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := &changeSet{NewState: cur}
	if max > 0 && len(rows) > max {
		rows = rows[:max]
		out.HasMore = true
		out.NewState = strconv.FormatInt(rows[len(rows)-1].Seq, 10)
	}
	// Collapse each object's entries to one: created wins over updated, and an
	// object created and destroyed in the window is not reported at all.
	first := map[string]string{}
	last := map[string]string{}
	var order []string
	for _, r := range rows {
		if _, ok := first[r.ObjectID]; !ok {
			first[r.ObjectID] = r.Kind
			order = append(order, r.ObjectID)
		}
		last[r.ObjectID] = r.Kind
	}
	out.Created, out.Updated, out.Destroyed = []string{}, []string{}, []string{}
	for _, id := range order {
		f, l := first[id], last[id]
		switch {
		case f == kindCreated && l == kindDestroyed:
		case l == kindDestroyed:
			out.Destroyed = append(out.Destroyed, id)
		case f == kindCreated:
			out.Created = append(out.Created, id)
		default:
			out.Updated = append(out.Updated, id)
		}
	}
	return out, nil
}

// prune drops entries older than maxAge and raises the floors accordingly.
func (l changeLog) prune(maxAge time.Duration) (int64, error) {
	cut := time.Now().UTC().Add(-maxAge)
	type floor struct {
		AccountID string
		Type      string
		Seq       int64
	}
	var floors []floor
	if err := l.db.Model(&model.ObjectChange{}).Select("account_id, type, MAX(seq) AS seq").
		Where("created_at < ?", cut).Group("account_id, type").Scan(&floors).Error; err != nil {
		return 0, err
	}
	var n int64
	err := l.db.Transaction(func(tx *gorm.DB) error {
		for _, f := range floors {
			if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"min_seq"})}).
				Create(&model.ObjectChangeFloor{AccountID: f.AccountID, Type: f.Type, MinSeq: f.Seq}).Error; err != nil {
				return err
			}
		}
		res := tx.Where("created_at < ?", cut).Delete(&model.ObjectChange{})
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}
