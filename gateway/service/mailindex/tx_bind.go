package mailindex

import (
	"gorm.io/gorm/clause"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Bind makes a message the server stored on its own (its copy of a sent draft)
// continue an existing email: emailID gets the membership (mailboxID, uid) and the
// copy's metadata, and keeps its id and thread. Memberships it already has stay
// until the caller removes them (RemoveUIDs), so a sync round that sees the copy
// first never leaves the email without a mailbox.
func (tx *Tx) Bind(emailID string, e *model.IndexEmail, mailboxID string, uid uint32, modseq uint64) error {
	var cur model.IndexEmail
	if err := tx.db.Select("id", "thread_id").Where("id = ?", emailID).First(&cur).Error; err != nil {
		return err
	}
	kw := ParseKeywords(e.Keywords)
	cols := map[string]any{
		"upstream_id":    e.UpstreamID,
		"size":           e.Size,
		"received_at":    e.ReceivedAt,
		"preview":        e.Preview,
		"has_attachment": e.HasAttachment,
		"body_structure": e.BodyStructure,
		"keywords":       kw.JSON(),
		"seen":           kw["$seen"],
		"flagged":        kw["$flagged"],
		"draft":          kw["$draft"],
		"answered":       kw["$answered"],
		"orphaned_from":  "",
	}
	if e.MessageID != "" {
		cols["message_id"] = e.MessageID // the gateway may have added it when sending
	}
	if err := tx.db.Model(&model.IndexEmail{}).Where("id = ?", emailID).Updates(cols).Error; err != nil {
		return err
	}
	if err := tx.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Membership{MailboxID: mailboxID, UID: uid, EmailID: emailID, ModSeq: modseq}).Error; err != nil {
		return err
	}
	tx.record(TypeEmail, emailID, KindUpdated)
	tx.record(TypeMailbox, mailboxID, KindCounts)
	return nil
}
