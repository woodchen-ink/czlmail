package submission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// EnvelopeAddress is a JMAP Address of an Envelope.
type EnvelopeAddress struct {
	Email      string             `json:"email"`
	Parameters map[string]*string `json:"parameters"`
}

// Envelope is the SMTP envelope of a submission.
type Envelope struct {
	MailFrom EnvelopeAddress   `json:"mailFrom"`
	RcptTo   []EnvelopeAddress `json:"rcptTo"`
}

// createArgs is one EmailSubmission/set create.
type createArgs struct {
	IdentityID string    `json:"identityId"`
	EmailID    string    `json:"emailId"`
	Envelope   *Envelope `json:"envelope"`
	// Server-set or unsupported properties a client may still send.
	UndoStatus *string `json:"undoStatus"`
	SendAt     *string `json:"sendAt"`
}

// deliveryStatus is a JMAP DeliveryStatus.
type deliveryStatus struct {
	SMTPReply string `json:"smtpReply"`
	Delivered string `json:"delivered"`
	Displayed string `json:"displayed"`
}

// sendTarget is what a submission needs to know about its upstream.
type sendTarget struct {
	accountID string
	handle    *service.Handle
	row       *model.Upstream
}

// create sends one submission. It returns the stored submission, or for a
// duplicate the earlier one with dup set, or a SetError.
func (s *Service) create(ctx context.Context, t *sendTarget, created map[string]string, args *createArgs, plan *emailPlan) (row *model.EmailSubmission, dup bool, serr setError) {
	row, serr = s.createOnce(ctx, t, created, args, plan, &dup)
	return row, dup, serr
}

func (s *Service) createOnce(ctx context.Context, t *sendTarget, created map[string]string, args *createArgs, plan *emailPlan, dupOut *bool) (*model.EmailSubmission, setError) {
	if args.UndoStatus != nil && *args.UndoStatus != "final" {
		return nil, invalidProperties("Delayed sending is not supported.", "undoStatus")
	}
	if args.SendAt != nil {
		return nil, invalidProperties("Delayed sending is not supported (maxDelayedSend is 0).", "sendAt")
	}
	emailID, ok := resolve(args.EmailID, created)
	if !ok || emailID == "" {
		return nil, invalidProperties("Unknown emailId.", "emailId")
	}
	if plan != nil {
		plan.emailID = emailID
	}
	identityID, ok := resolve(args.IdentityID, created)
	if !ok || identityID == "" {
		return nil, invalidProperties("Unknown identityId.", "identityId")
	}
	idents, err := s.Identities.List(t.accountID)
	if err != nil {
		return nil, newSetError("serverFail", "")
	}
	var ident *model.Identity
	for i := range idents {
		if idents[i].PublicID == identityID {
			ident = &idents[i]
		}
	}
	if ident == nil {
		return nil, invalidProperties("Unknown identityId.", "identityId")
	}

	idx := t.handle.Upstream.Index()
	var email model.IndexEmail
	if err := idx.DB().Select("id", "thread_id").Where("id = ? AND orphaned_from = ''", emailID).First(&email).Error; err != nil {
		return nil, invalidProperties("The email does not exist.", "emailId")
	}
	var buf bytes.Buffer
	if err := t.handle.Upstream.FetchRaw(ctx, emailID, &limitWriter{w: &buf, left: MaxMessageSize}); err != nil {
		if errors.Is(err, errTooLarge) {
			return nil, newSetError("tooLarge", "The message is larger than the gateway sends.")
		}
		s.log.Warn("fetch draft for sending", "account", t.accountID, "email", emailID, "err", err)
		return nil, upstreamSetError(err)
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])

	h, herr := parseHeader(raw)

	// Duplicate guard: the same content again, or the same message after the
	// server's Sent copy took over the draft's id (its bytes differ, its
	// Message-ID does not).
	unlock := s.lockKey(t.accountID + "\x00" + emailID)
	defer unlock()
	var dup model.EmailSubmission
	q := s.db.Where("account_id = ? AND email_id = ? AND created_at > ?", t.accountID, emailID, time.Now().UTC().Add(-DuplicateWindow))
	if herr == nil && h.MessageID != "" {
		q = q.Where("content_hash = ? OR message_id = ?", hash, h.MessageID)
	} else {
		q = q.Where("content_hash = ?", hash)
	}
	if q.Order("id DESC").First(&dup).Error == nil {
		s.log.Info("duplicate submission answered with the first one", "account", t.accountID, "email", emailID, "submission", dup.PublicID)
		*dupOut = true
		return &dup, nil
	}
	if herr != nil {
		return nil, newSetError("invalidEmail", "The email's header cannot be parsed.")
	}
	if len(h.From) == 0 {
		return nil, newSetError("forbiddenFrom", "The email has no From address.")
	}
	for _, a := range append(append([]*mail.Address{}, h.From...), h.Sender...) {
		if !covered(idents, a.Address) {
			return nil, newSetError("forbiddenFrom", "The From or Sender address is not one of this account's identities.")
		}
	}
	env, serr := envelopeOf(args.Envelope, h)
	if serr != nil {
		return nil, serr
	}
	if !covered(idents, env.MailFrom.Email) {
		return nil, newSetError("forbiddenMailFrom", "The envelope sender is not one of this account's identities.")
	}

	threadID := email.ThreadID
	if ms, ok := t.handle.Upstream.(upstream.MessageSender); ok {
		// A provider API (Gmail): recipients come from the header, the provider
		// strips Bcc and files the Sent copy under the draft's id itself.
		if derived, _ := envelopeOf(nil, h); !sameRecipients(env, derived) {
			return nil, invalidProperties("This mailbox sends to the header's recipients only.", "envelope")
		}
		res, err := ms.SendMessage(ctx, raw, upstream.SendOptions{DraftEmailID: emailID})
		if err != nil {
			s.log.Warn("send failed", "account", t.accountID, "email", emailID, "err", err)
			return nil, s.sendSetError(t.accountID, err)
		}
		s.mgr.Publish(t.accountID, res.States)
		if res.EmailID != "" {
			emailID = res.EmailID
			if plan != nil {
				plan.emailID = emailID
			}
		}
		if res.ThreadID != "" {
			threadID = res.ThreadID
		}
		return s.record(t, identityID, emailID, threadID, hash, h.MessageID, env)
	}

	now := time.Now()
	msgID := messageID(h, domainOf(h.From[0].Address))
	out := outgoing(raw, h, msgID, now, true)
	msg := &upstream.Outgoing{EmailID: emailID, MailFrom: env.MailFrom.Email, Raw: out, MessageID: msgID}
	for _, r := range env.RcptTo {
		msg.RcptTo = append(msg.RcptTo, r.Email)
	}
	sender, ok := t.handle.Upstream.(upstream.Sender)
	if !ok {
		return nil, newSetError("forbiddenToSend", "This account cannot send mail.")
	}
	if plan != nil {
		plan.messageID = msgID
		if h.MessageID == "" || !h.HasDate {
			// Sent shows what was delivered (with the added headers; Bcc kept).
			plan.ownCopy = outgoing(raw, h, msgID, now, false)
		}
		plan.prepare(t)
	}
	if _, err := s.send(ctx, sender, msg); err != nil {
		if plan != nil {
			plan.cancel()
		}
		var se *upstream.SendError
		if errors.As(err, &se) {
			s.log.Warn("send refused", "account", t.accountID, "email", emailID, "stage", se.Stage, "code", se.Code, "reply", se.Reply, "err", se.Err)
		} else {
			s.log.Warn("send failed", "account", t.accountID, "email", emailID, "err", err)
		}
		return nil, s.sendSetError(t.accountID, err)
	}

	return s.record(t, identityID, emailID, threadID, hash, msgID, env)
}

// record stores a sent submission and logs its creation.
func (s *Service) record(t *sendTarget, identityID, emailID, threadID, hash, msgID string, env *Envelope) (*model.EmailSubmission, setError) {
	status := map[string]deliveryStatus{}
	for _, r := range env.RcptTo {
		status[r.Email] = deliveryStatus{SMTPReply: "250 2.0.0 Accepted", Delivered: "queued", Displayed: "unknown"}
	}
	envJSON, _ := json.Marshal(env)
	statusJSON, _ := json.Marshal(status)
	now := time.Now().UTC()
	row := &model.EmailSubmission{
		PublicID: newID("s"), AccountID: t.accountID, IdentityID: identityID, EmailID: emailID, ThreadID: threadID,
		Envelope: string(envJSON), SendAt: now, UndoStatus: "final", DeliveryStatus: string(statusJSON), ContentHash: hash, MessageID: msgID, CreatedAt: now,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return s.changes.record(tx, t.accountID, TypeEmailSubmission, row.PublicID, kindCreated)
	})
	if err != nil {
		// Sent but not recorded: the client must not retry, so report success.
		s.log.Error("store submission", "account", t.accountID, "email", emailID, "err", err)
	}
	return row, nil
}

// sameRecipients compares envelope recipient sets, case-insensitively.
func sameRecipients(a, b *Envelope) bool {
	if a == nil || b == nil || len(a.RcptTo) != len(b.RcptTo) {
		return false
	}
	set := map[string]bool{}
	for _, r := range a.RcptTo {
		set[strings.ToLower(r.Email)] = true
	}
	for _, r := range b.RcptTo {
		if !set[strings.ToLower(r.Email)] {
			return false
		}
	}
	return true
}

// send delivers msg, retrying temporary failures.
func (s *Service) send(ctx context.Context, sender upstream.Sender, msg *upstream.Outgoing) (*upstream.DeliveryResult, error) {
	for attempt := 0; ; attempt++ {
		res, err := sender.Send(ctx, msg)
		var se *upstream.SendError
		if err == nil || !errors.As(err, &se) || !se.Temporary() || attempt >= len(s.opts.RetryDelays) {
			return res, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(s.opts.RetryDelays[attempt]):
		}
	}
}

// envelopeOf validates the client's envelope or derives one from the header:
// Sender (else the first From) and every To, Cc and Bcc address once.
func envelopeOf(in *Envelope, h *header) (*Envelope, setError) {
	if in != nil {
		if len(in.MailFrom.Parameters) > 0 {
			return nil, invalidProperties("SMTP parameters are not supported.", "envelope")
		}
		if _, err := mail.ParseAddress(in.MailFrom.Email); err != nil {
			return nil, invalidProperties("Invalid envelope sender.", "envelope")
		}
		out := &Envelope{MailFrom: EnvelopeAddress{Email: in.MailFrom.Email}}
		seen := map[string]bool{}
		for _, r := range in.RcptTo {
			if len(r.Parameters) > 0 {
				return nil, invalidProperties("SMTP parameters are not supported.", "envelope")
			}
			if _, err := mail.ParseAddress(r.Email); err != nil {
				e := newSetError("invalidRecipients", "Invalid recipient address.")
				e["invalidRecipients"] = []string{r.Email}
				return nil, e
			}
			if k := strings.ToLower(r.Email); !seen[k] {
				seen[k] = true
				out.RcptTo = append(out.RcptTo, EnvelopeAddress{Email: r.Email})
			}
		}
		return checkRecipients(out)
	}
	from := h.From[0].Address
	if len(h.Sender) > 0 {
		from = h.Sender[0].Address
	}
	out := &Envelope{MailFrom: EnvelopeAddress{Email: from}}
	seen := map[string]bool{}
	for _, list := range [][]*mail.Address{h.To, h.Cc, h.Bcc} {
		for _, a := range list {
			if k := strings.ToLower(a.Address); a.Address != "" && !seen[k] {
				seen[k] = true
				out.RcptTo = append(out.RcptTo, EnvelopeAddress{Email: a.Address})
			}
		}
	}
	return checkRecipients(out)
}

func checkRecipients(e *Envelope) (*Envelope, setError) {
	switch {
	case len(e.RcptTo) == 0:
		return nil, newSetError("noRecipients", "The message has no recipients.")
	case len(e.RcptTo) > MaxRecipients:
		se := newSetError("tooManyRecipients", "The message has too many recipients.")
		se["maxRecipients"] = MaxRecipients
		return nil, se
	}
	return e, nil
}

// resolve turns a "#creationId" reference into the id created earlier in the request.
func resolve(id string, created map[string]string) (string, bool) {
	if ref, ok := strings.CutPrefix(id, "#"); ok {
		v, found := created[ref]
		return v, found
	}
	return id, true
}

var errTooLarge = errors.New("message too large")

type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errTooLarge
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}
