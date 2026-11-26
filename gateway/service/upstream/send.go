package upstream

import (
	"context"
	"errors"
	"net/textproto"
	"time"
)

// Outgoing is one message to deliver.
type Outgoing struct {
	// EmailID is the draft being sent (for logs and the Sent copy).
	EmailID string
	// MailFrom and RcptTo are the SMTP envelope.
	MailFrom string
	RcptTo   []string
	// Raw is the message as delivered: Bcc removed, Message-ID and Date present.
	Raw []byte
	// MessageID is Raw's Message-ID without angle brackets.
	MessageID string
}

// RecipientResult is what the server answered for one RCPT TO.
type RecipientResult struct {
	Accepted bool
	Code     int
	// Reply is the server's own text, for logs and the admin only.
	Reply string
}

// DeliveryResult is a delivery the SMTP server accepted.
type DeliveryResult struct {
	// Recipients by envelope address, as given in Outgoing.RcptTo.
	Recipients map[string]RecipientResult
	// Reply is the server's final answer to the message data (admin only).
	Reply string
}

// Sender delivers mail over SMTP with an explicit envelope (the IMAP upstream,
// through DialSMTP); the Sent copy is then filed through SentFiler. Upstreams
// that send through a provider API implement MessageSender (optional.go)
// instead, which also files the Sent copy.
type Sender interface {
	Send(ctx context.Context, msg *Outgoing) (*DeliveryResult, error)
}

// SMTP stages a SendError can come from.
const (
	StageConnect = "connect" // dial, greeting, EHLO, TLS, AUTH
	StageMail    = "mail"
	StageRcpt    = "rcpt"
	StageData    = "data"
)

// SendError is a delivery the server refused. Code is the SMTP reply code (0 for
// network failures); Reply and Err are the server's words, never shown to clients.
type SendError struct {
	Stage string
	Code  int
	Reply string
	// Rejected lists the recipients refused with a permanent (5xx) RCPT reply.
	Rejected map[string]RecipientResult
	Err      error
}

func (e *SendError) Error() string {
	if e.Err != nil {
		return "smtp " + e.Stage + ": " + e.Err.Error()
	}
	return "smtp " + e.Stage + ": " + e.Reply
}
func (e *SendError) Unwrap() error { return e.Err }

// Temporary reports a 4xx reply or a network failure worth retrying.
func (e *SendError) Temporary() bool { return e.Code >= 400 && e.Code < 500 }

// SMTPCode extracts the reply code of an SMTP error (0 when there is none).
func SMTPCode(err error) (int, string) {
	var tp *textproto.Error
	if errors.As(err, &tp) {
		return tp.Code, tp.Msg
	}
	return 0, ""
}

// Sent-copy strategies of an upstream (model.Upstream.SentCopy).
const (
	// SentCopyAuto has not been decided yet: the next send waits for a server copy
	// and remembers what it saw.
	SentCopyAuto = "auto"
	// SentCopyGateway: the server does not keep sent mail; the draft is moved to Sent.
	SentCopyGateway = "gateway"
	// SentCopyServer: the server saves its own copy; the draft is deleted and the
	// server's copy takes over the draft's Email id.
	SentCopyServer = "server"
)

// SentCopyWait is how long the gateway waits for a server's own Sent copy.
const SentCopyWait = 15 * time.Second

// LateCopyWindow is how long after a timeout a late server copy still replaces
// the gateway's own.
const LateCopyWindow = time.Hour

// FileSentRequest files a sent draft in the Sent mailbox, keeping its Email id.
type FileSentRequest struct {
	DraftID       string
	SentMailboxID string
	MessageID     string // of the delivered message, without angle brackets
	Mode          string // SentCopyGateway, SentCopyServer or SentCopyAuto
	// Keywords to add and remove on the filed message ($draft off, $seen on, ...).
	AddKeywords, RemoveKeywords []string
	// OwnCopy, when set, is what the gateway files as its own copy instead of
	// moving the draft: the delivered bytes when they differ from the draft (the
	// gateway added a Message-ID or Date), so Sent shows what was sent and a late
	// server copy can still be matched by Message-ID.
	OwnCopy []byte
	// Wait overrides SentCopyWait (tests).
	Wait time.Duration
	// Learned is called with what the server did when Mode is auto, and again with
	// true if a server copy turns up after the gateway filed its own.
	Learned func(serverSaves bool)
}

// FileSentResult says how the message was filed.
type FileSentResult struct {
	// ServerCopy is true when the server's own copy now carries the Email id.
	ServerCopy bool
	States     map[string]string
}

// SentFiler is implemented by upstreams that can make the sent message the one
// Sent copy under the draft's Email id.
type SentFiler interface {
	// PrepareSent announces the draft before it is sent, so a sync round that sees
	// the server's copy first binds it instead of creating a new email. The
	// returned cancel undoes it when sending fails.
	PrepareSent(draftID, sentMailboxID, messageID string) (cancel func())
	FileSent(ctx context.Context, req FileSentRequest) (*FileSentResult, error)
}

// SentCopyTester sends a test message to the upstream's own address and reports
// whether the server stored a copy in Sent within wait. The test message is
// removed from Sent (and from the inbox when it arrived) afterwards.
type SentCopyTester interface {
	TestSentCopy(ctx context.Context, wait time.Duration) (bool, error)
}
