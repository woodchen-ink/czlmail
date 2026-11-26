package jmapserver

import (
	"context"
	"encoding/json"
	"time"
)

// Push (RFC 8620 §7.2) and new-mail push (draft-ietf-jmap-emailpush).
const (
	// EmailPushCapability is announced on the session (an empty object) and on
	// every account that has MailCapability while push is enabled.
	EmailPushCapability = "urn:ietf:params:jmap:emailpush"
	// MailCapability is RFC 8621's capability; new-mail push only applies to accounts that have it.
	MailCapability = "urn:ietf:params:jmap:mail"

	// MaxPushTTL caps a subscription's expires, as Stalwart does; clients renew before it runs out.
	MaxPushTTL = 7 * 24 * time.Hour
	// MaxPushSubscriptions is how many subscriptions one user may hold (one per device and sign-in).
	MaxPushSubscriptions = 32
	// DefaultMaxPushPayload is the plaintext budget of one push message. The CZL Connect relay
	// puts the ciphertext base64url-encoded into an APNs payload of at most 4096 bytes and drops
	// it when it does not fit, so the ciphertext must stay under about 2900 bytes; RFC 8291 adds
	// 103 bytes (header, delimiter, tag) and the rest is margin for the localized alert text.
	DefaultMaxPushPayload = 2600
	// maxFilterDepth is the account capability's maxFilterDepth.
	maxFilterDepth = 4
)

// PushSubscription is a stored subscription. The engine creates and changes it;
// a PushStore only persists it.
type PushSubscription struct {
	ID     string
	UserID string
	// CredentialID is the credential (app password) the subscription was created
	// with, when the Authenticator reports one; revoking it deletes the subscription.
	CredentialID   string
	DeviceClientID string
	URL            string
	// P256DH (65-byte uncompressed point) and Auth (16 bytes) are nil when the
	// client gave no keys; messages are then sent unencrypted.
	P256DH []byte
	Auth   []byte
	// VerificationCode is the code pushed in the PushVerification; Verified is set
	// once the client echoes it back. Nothing else is pushed before that.
	VerificationCode string
	Verified         bool
	Expires          time.Time
	// Types limits StateChange pushes; nil means all types, empty means none.
	Types []string
	// EmailPush is the emailpush configuration by accountId; nil when unset.
	EmailPush map[string]EmailPushConfig
	CreatedAt time.Time
}

// EmailPushConfig is one account's entry in a subscription's emailPush.
type EmailPushConfig struct {
	Filter     json.RawMessage `json:"filter"`
	Properties []string        `json:"properties"`
	Urgency    string          `json:"urgency"`
}

// PushStore persists push subscriptions; the gateway keeps them in gateway.sqlite.
type PushStore interface {
	// ListPush returns all of the user's subscriptions, expired ones included.
	ListPush(ctx context.Context, userID string) ([]*PushSubscription, error)
	CreatePush(ctx context.Context, sub *PushSubscription) error
	// UpdatePush saves the mutable fields: VerificationCode, Verified, Expires, Types, EmailPush.
	UpdatePush(ctx context.Context, sub *PushSubscription) error
	// DeletePush deletes the user's subscriptions with the given ids; unknown ids are ignored.
	DeletePush(ctx context.Context, userID string, ids ...string) error
	// PushUsers lists the users that have at least one subscription.
	PushUsers(ctx context.Context) ([]string, error)
}

// EmailAddress is an RFC 8621 EmailAddress.
type EmailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// NewMail is a message just delivered into an account, as the mail layer reports
// it to Server.NewMail. Only deliveries count (a message synchronised in from the
// upstream mailbox), never messages created through Email/set, Email/import or
// IMAP APPEND by the user. The typed fields are what push filters look at; Email
// is the payload.
type NewMail struct {
	AccountID  string
	MailboxIDs []string
	Keywords   []string
	From       []EmailAddress
	To         []EmailAddress
	Cc         []EmailAddress
	Bcc        []EmailAddress
	Subject    string
	// Email holds the message's properties in the JSON shape of Email/get, keyed
	// by property name; it must cover EmailPushProperties. Values must marshal to JSON.
	Email map[string]any
	// State is the account's Email state after this message was added; empty when unknown.
	State string
}

// EmailPushProperties are the Email properties a subscription may ask to be pushed.
var EmailPushProperties = []string{
	"id", "blobId", "threadId", "mailboxIds", "keywords", "size", "receivedAt",
	"messageId", "inReplyTo", "references", "sender", "from", "to", "cc", "bcc", "replyTo",
	"subject", "sentAt", "hasAttachment", "preview",
}

// emailPushFilterConditions are the FilterCondition properties push filters support.
var emailPushFilterConditions = []string{
	"inMailbox", "inMailboxOtherThan", "hasKeyword", "notKeyword", "from", "to", "cc", "bcc", "subject",
}

// NewMailNotifier is what the mail layer needs from the server to report
// deliveries; *Server implements it.
type NewMailNotifier interface {
	NewMail(userID string, mails ...NewMail)
}

// emailPushAccountCapability is the account-level capability value.
func emailPushAccountCapability() map[string]any {
	return map[string]any{
		"filterConditions": emailPushFilterConditions,
		"maxFilterDepth":   maxFilterDepth,
		"countProperties":  []string{}, // EmailCount pushes are not supported
	}
}
