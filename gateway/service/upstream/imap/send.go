package imap

import (
	"context"
	"errors"
	"fmt"
	"net/smtp"
	"strconv"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// sendTimeout bounds one SMTP transaction.
const sendTimeout = 2 * time.Minute

// Send delivers a message over the upstream's SMTP server (through its proxy).
// Every permanent RCPT refusal aborts the transaction before DATA, so a message
// either reaches every recipient or none.
func (u *Upstream) Send(ctx context.Context, msg *upstream.Outgoing) (*upstream.DeliveryResult, error) {
	if u.cfg.SMTP.Host == "" {
		return nil, &upstream.SendError{Stage: upstream.StageConnect, Err: errors.New("no SMTP server configured")}
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c, _, err := DialSMTP(ctx, u.cfg)
	if err != nil {
		code, reply := upstream.SMTPCode(err)
		return nil, &upstream.SendError{Stage: upstream.StageConnect, Code: code, Reply: reply, Err: err}
	}
	defer c.Close()
	res, err := transact(c, msg)
	if err != nil {
		u.log.Warn("smtp send failed", "email", msg.EmailID, "err", err)
		return nil, err
	}
	_ = c.Quit()
	u.log.Info("smtp send accepted", "email", msg.EmailID, "recipients", len(msg.RcptTo))
	return res, nil
}

func transact(c *smtp.Client, msg *upstream.Outgoing) (*upstream.DeliveryResult, error) {
	if ok, param := c.Extension("SIZE"); ok {
		if limit, err := strconv.ParseInt(param, 10, 64); err == nil && limit > 0 && int64(len(msg.Raw)) > limit {
			return nil, &upstream.SendError{Stage: upstream.StageMail, Code: 552,
				Reply: fmt.Sprintf("message is %d bytes, the server accepts %d", len(msg.Raw), limit)}
		}
	}
	if err := c.Mail(msg.MailFrom); err != nil {
		return nil, stageError(upstream.StageMail, err)
	}
	res := &upstream.DeliveryResult{Recipients: map[string]upstream.RecipientResult{}}
	rejected := map[string]upstream.RecipientResult{}
	var temporary error
	for _, rcpt := range msg.RcptTo {
		err := c.Rcpt(rcpt)
		if err == nil {
			res.Recipients[rcpt] = upstream.RecipientResult{Accepted: true, Code: 250}
			continue
		}
		code, reply := upstream.SMTPCode(err)
		switch {
		case code >= 500:
			rejected[rcpt] = upstream.RecipientResult{Code: code, Reply: reply}
		case code >= 400:
			if temporary == nil {
				temporary = err
			}
			res.Recipients[rcpt] = upstream.RecipientResult{Code: code, Reply: reply}
		default:
			return nil, stageError(upstream.StageRcpt, err) // the connection broke
		}
	}
	if len(rejected) > 0 || temporary != nil {
		_ = c.Reset()
		se := stageError(upstream.StageRcpt, temporary)
		if len(rejected) > 0 {
			se.Rejected = rejected
			for _, r := range rejected {
				se.Code, se.Reply = r.Code, r.Reply
				break
			}
		}
		return nil, se
	}
	w, err := c.Data()
	if err != nil {
		return nil, stageError(upstream.StageData, err)
	}
	if _, err := w.Write(msg.Raw); err != nil {
		w.Close()
		return nil, stageError(upstream.StageData, err)
	}
	if err := w.Close(); err != nil {
		return nil, stageError(upstream.StageData, err)
	}
	res.Reply = "250"
	return res, nil
}

func stageError(stage string, err error) *upstream.SendError {
	se := &upstream.SendError{Stage: stage, Err: err}
	if err != nil {
		se.Code, se.Reply = upstream.SMTPCode(err)
	}
	return se
}
