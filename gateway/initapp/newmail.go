package initapp

import (
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mail"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// newMailNotifier hands emails delivered into an upstream to push
// notifications. The Email state change itself is published by the upstream
// manager like any other index commit.
func newMailNotifier(jmap *jmapserver.Server) func(userID string, mails []mail.NewMail) {
	return func(userID string, mails []mail.NewMail) {
		out := make([]jmapserver.NewMail, 0, len(mails))
		for _, m := range mails {
			out = append(out, jmapserver.NewMail{
				AccountID: m.AccountID, MailboxIDs: m.MailboxIDs, Keywords: m.Keywords,
				From: addrs(m.From), To: addrs(m.To), Cc: addrs(m.Cc), Bcc: addrs(m.Bcc),
				Subject: m.Subject, Email: m.Email, State: m.State,
			})
		}
		jmap.NewMail(userID, out...)
	}
}

func addrs(list []mailindex.Address) []jmapserver.EmailAddress {
	out := make([]jmapserver.EmailAddress, len(list))
	for i, a := range list {
		out[i] = jmapserver.EmailAddress{Name: a.Name, Email: a.Email}
	}
	return out
}
