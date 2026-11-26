package service

import (
	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// deleteAccountRows removes what gateway.sqlite keeps per JMAP account besides
// the upstream row (identities, submissions, their changelog), in tx.
func deleteAccountRows(tx *gorm.DB, accountIDs []string) error {
	if len(accountIDs) == 0 {
		return nil
	}
	for _, m := range []any{&model.Identity{}, &model.EmailSubmission{}, &model.ObjectChange{}, &model.ObjectChangeFloor{}} {
		if err := tx.Where("account_id IN ?", accountIDs).Delete(m).Error; err != nil {
			return err
		}
	}
	return nil
}
