package mail

import (
	"context"
	"slices"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// NewMail is an email that just arrived on an upstream, for push
// notifications (jmapserver.Server.NewMail of #45 takes the same fields).
type NewMail struct {
	AccountID  string
	MailboxIDs []string
	Keywords   []string
	From       []mailindex.Address
	To         []mailindex.Address
	Cc         []mailindex.Address
	Bcc        []mailindex.Address
	Subject    string
	// Email holds the index-only properties in the JSON shape of Email/get.
	Email map[string]any
	// State is the account's Email state after the delivery.
	State string
}

// pushProperties are the Email properties a push may carry; all come from the index.
var pushProperties = []string{"id", "blobId", "threadId", "mailboxIds", "keywords", "size", "receivedAt",
	"messageId", "inReplyTo", "references", "sender", "from", "to", "cc", "bcc", "replyTo",
	"subject", "sentAt", "hasAttachment", "preview"}

// quietRoles are folders whose new messages are not "new mail": copies the
// user's own clients store (sent, drafts) and what the server filed away.
var quietRoles = []string{"sent", "drafts", "trash", "junk"}

// Delivered is the upstream manager's delivery hook (service.DeliveryFunc). It
// runs under the index write lock, so the work happens on another goroutine.
func (s *Service) Delivered(userID, accountID string, idx *mailindex.Store, emailIDs []string) {
	if s.opts.OnNewMail == nil || len(emailIDs) == 0 {
		return
	}
	ids := slices.Clone(emailIDs)
	go s.deliver(userID, accountID, idx, ids)
}

func (s *Service) deliver(userID, accountID string, idx *mailindex.Store, ids []string) {
	msgs, err := loadMessages(idx, ids)
	if err != nil {
		s.log.Warn("new mail notification", "account", accountID, "err", err)
		return
	}
	var quiet []string
	if err := idx.DB().Model(&model.IndexMailbox{}).Where("role IN ?", quietRoles).Pluck("id", &quiet).Error; err != nil {
		s.log.Warn("new mail notification", "account", accountID, "err", err)
		return
	}
	state, _ := idx.State(mailindex.TypeEmail)
	props, _ := parseProps(&pushProperties, emailProperties, nil, "id")
	g := &getter{s: s, a: &account{id: accountID, idx: idx}, props: props, bprops: &propSet{names: map[string]bool{}}, args: &emailGetArgs{}}
	var out []NewMail
	for _, id := range ids {
		m := msgs[id]
		if m == nil || len(m.mailboxes) == 0 || slices.ContainsFunc(m.mailboxes, func(b string) bool { return slices.Contains(quiet, b) }) {
			continue
		}
		obj, err := g.build(context.Background(), m)
		if err != nil {
			continue
		}
		kw := keywordList(keywordsOf(m.row.Keywords))
		out = append(out, NewMail{
			AccountID: accountID, MailboxIDs: m.mailboxes, Keywords: kw,
			From: mailindex.DecodeAddresses(m.row.From), To: mailindex.DecodeAddresses(m.row.To),
			Cc: mailindex.DecodeAddresses(m.row.Cc), Bcc: mailindex.DecodeAddresses(m.row.Bcc),
			Subject: m.row.Subject, Email: obj, State: state,
		})
	}
	if len(out) > 0 {
		s.opts.OnNewMail(userID, out)
	}
}
