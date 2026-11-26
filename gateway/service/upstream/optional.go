package upstream

import (
	"context"
	"time"
)

// Optional interfaces. JMAP methods check for them with a type assertion; an
// upstream that does not implement one gets the generic behaviour.

// MailboxSetter is implemented by upstreams whose emails can be in several
// mailboxes at once (Gmail labels; Session maxMailboxesPerEmail is null). Email/set
// changes of mailboxIds go here instead of Move: add and remove are mailbox ids
// applied to every email. Moving to Trash keeps what is needed to restore the
// email's mailboxes when it leaves Trash again.
type MailboxSetter interface {
	SetMailboxes(ctx context.Context, emailIDs []string, add, remove []string) (map[string]string, error)
}

// SearchFilter is the part of an RFC 8621 FilterCondition a server can search
// in message text; the caller evaluates mailbox, keyword and the rest from the
// index. Empty fields are not set.
type SearchFilter struct {
	Text, From, To, Cc, Bcc, Subject, Body string
	Before, After                          *time.Time // receivedAt
	MinSize, MaxSize                       int64
	HasAttachment                          *bool
}

// TextSearcher is implemented by upstreams that search on the server (Gmail:
// X-GM-RAW). It returns the ids of matching emails in the index, in no order.
type TextSearcher interface {
	SearchText(ctx context.Context, f SearchFilter) ([]string, error)
}

// SendOptions go with one message.
type SendOptions struct {
	// DraftEmailID is the draft being sent, "" for none. After sending, the draft
	// is removed and the sent message takes its Email id.
	DraftEmailID string
	// InReplyToEmailID is the email being answered, for the provider's
	// conversation (Gmail threadId); "" lets the provider decide.
	InReplyToEmailID string
}

// SendResult is a sent message as it is now in the index.
type SendResult struct {
	EmailID  string
	ThreadID string
	States   map[string]string
}

// MessageSender is implemented by upstreams that send through the provider's
// API instead of SMTP (Gmail users.messages.send). Recipients come from the
// message's To / Cc / Bcc headers (the API takes no envelope; Bcc is removed
// by the provider). The provider stores the message in Sent itself: callers
// must not Append it.
type MessageSender interface {
	SendMessage(ctx context.Context, raw []byte, opts SendOptions) (*SendResult, error)
}

// Identity is one address the upstream may send as (RFC 8621 Identity).
type Identity struct {
	ID            string
	Name          string
	Email         string
	ReplyTo       string // "" for none
	HTMLSignature string
	MayDelete     bool
}

// IdentitySource is implemented by upstreams that know their send-as
// addresses (Gmail settings.sendAs.list). The list may be cached by the
// upstream; ids are stable for an address.
type IdentitySource interface {
	Identities(ctx context.Context) ([]Identity, error)
}
