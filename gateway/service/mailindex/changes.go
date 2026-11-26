package mailindex

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// ErrCannotCalculateChanges means the state is malformed, from another generation
// of the index, or older than the trimmed changelog.
var ErrCannotCalculateChanges = errors.New("mailindex: cannot calculate changes")

// Changes is the result of a /changes computation (RFC 8620 §5.2).
type Changes struct {
	OldState       string
	NewState       string
	HasMoreChanges bool
	Created        []string
	Updated        []string
	Destroyed      []string
	// CountsOnly is true when every updated Mailbox only changed its counts, so
	// Mailbox/changes can report updatedProperties (RFC 8621 §2.2).
	CountsOnly bool
}

// Changes lists what changed for typ since sinceState, at most max ids (0: no limit).
func (s *Store) Changes(typ, sinceState string, max int) (*Changes, error) {
	meta := s.Meta()
	if !meta.Ready {
		return nil, ErrNotReady
	}
	gen, since, ok := ParseState(sinceState)
	// The current state stays valid however much was trimmed: nothing changed since.
	if !ok || gen != meta.Generation || since > typeSeq(&meta, typ) || since < meta.MinSeq && since != typeSeq(&meta, typ) {
		return nil, ErrCannotCalculateChanges
	}
	out := &Changes{OldState: sinceState, NewState: formatState(gen, typeSeq(&meta, typ)), CountsOnly: true}
	byID := map[string]*agg{}
	var order []string
	// One read transaction: a concurrent Trim cannot delete entries between the
	// MinSeq check and the pages.
	err := s.db.Transaction(func(db *gorm.DB) error {
		var cur model.IndexMeta
		if err := db.Select("min_seq").First(&cur, 1).Error; err != nil {
			return err
		}
		if since < cur.MinSeq && since != typeSeq(&meta, typ) {
			return ErrCannotCalculateChanges
		}
		cursor := since
		const page = 1000
		for {
			var rows []model.IndexChange
			err := db.Where("type = ? AND seq > ? AND seq <= ?", typ, cursor, typeSeq(&meta, typ)).
				Order("seq").Limit(page).Find(&rows).Error
			if err != nil {
				return err
			}
			for _, r := range rows {
				a := byID[r.ObjectID]
				if a == nil {
					if max > 0 && len(order) >= max {
						out.HasMoreChanges = true
						out.NewState = formatState(gen, cursor)
						return nil
					}
					a = &agg{first: r.Kind}
					byID[r.ObjectID] = a
					order = append(order, r.ObjectID)
				}
				a.last = r.Kind
				if r.Kind == KindDestroyed {
					a.destroyed = true
				}
				if r.Kind == KindUpdated {
					out.CountsOnly = false
				}
				cursor = r.Seq
			}
			if len(rows) < page {
				return nil
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return finish(out, byID, order), nil
}

// agg is one id's entries in the window. Ids are never reused, so once
// destroyed an object stays destroyed whatever follows.
type agg struct {
	first, last string
	destroyed   bool
}

func finish(out *Changes, byID map[string]*agg, order []string) *Changes {
	for _, id := range order {
		a := byID[id]
		switch {
		case a.first == KindCreated && a.destroyed:
			// created and destroyed within the window: never seen
		case a.first == KindCreated:
			out.Created = append(out.Created, id)
		case a.destroyed:
			out.Destroyed = append(out.Destroyed, id)
		default:
			out.Updated = append(out.Updated, id)
		}
	}
	if out.Created == nil {
		out.Created = []string{}
	}
	if out.Updated == nil {
		out.Updated = []string{}
		out.CountsOnly = false
	}
	if out.Destroyed == nil {
		out.Destroyed = []string{}
	}
	return out
}

// Trim deletes changelog entries older than maxAge and beyond the newest
// maxEntries, and moves MinSeq so states before the cut get cannotCalculateChanges.
func (s *Store) Trim(maxAge time.Duration, maxEntries int64) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	meta := s.Meta()
	cut := meta.Seq - maxEntries // delete seq <= cut
	var oldest struct{ Seq int64 }
	err := s.db.Model(&model.IndexChange{}).Select("COALESCE(MAX(seq), 0) AS seq").
		Where("created_at < ?", time.Now().UTC().Add(-maxAge)).Scan(&oldest).Error
	if err != nil {
		return 0, err
	}
	cut = max(cut, oldest.Seq)
	if cut <= meta.MinSeq {
		return 0, nil
	}
	var deleted int64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("seq <= ?", cut).Delete(&model.IndexChange{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return tx.Model(&model.IndexMeta{}).Where("id = 1").Update("min_seq", cut).Error
	})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.meta.MinSeq = cut
	s.mu.Unlock()
	return deleted, nil
}
