package submission

import (
	"errors"
	"slices"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// setError is a JMAP SetError with optional extra members.
type setError map[string]any

func newSetError(typ, description string) setError {
	e := setError{"type": typ}
	if description != "" {
		e["description"] = description
	}
	return e
}

func invalidProperties(description string, props ...string) setError {
	e := newSetError("invalidProperties", description)
	if len(props) > 0 && props[0] != "" {
		e["properties"] = props
	}
	return e
}

func unavailable(status string, code int) setError {
	e := newSetError("serverUnavailable", "The mailbox server is unavailable; retry later.")
	e["czlUpstream"] = &jmapserver.UpstreamError{Status: status, Code: code}
	return e
}

// upstreamSetError maps a failure of an upstream read or write (fetching the
// draft, filing it) to a SetError.
func upstreamSetError(err error) setError {
	if upstream.IsKind(err, upstream.KindRejected) {
		return invalidProperties("The email cannot be read from the mailbox server.", "emailId")
	}
	st, code := upstream.Classify("", err).Status()
	return unavailable(st, code)
}

// sendSetError maps a refused delivery. The server's reply text is never part
// of it; the caller logs it.
func (s *Service) sendSetError(accountID string, err error) setError {
	var se *upstream.SendError
	if !errors.As(err, &se) {
		// A provider API (upstream.MessageSender) reports classified errors.
		ue := upstream.Classify("send", err)
		switch ue.Kind {
		case upstream.KindAuth:
			s.mgr.Fail(accountID, ue)
			return unavailable(jmapserver.UpstreamAuthRequired, jmapserver.CodeUpstreamAuthRequired)
		case upstream.KindThrottled:
			return newSetError("rateLimit", "The mail server is not accepting mail right now; retry later.")
		case upstream.KindRejected:
			return newSetError("forbiddenToSend", "The mail server refused the message.")
		}
		st, code := ue.Status()
		return unavailable(st, code)
	}
	switch {
	case se.Code == 535 || upstream.IsKind(se.Err, upstream.KindAuth):
		// The mailbox needs signing in again: the runner turns authRequired.
		s.mgr.Fail(accountID, &upstream.Error{Kind: upstream.KindAuth, Op: "smtp auth", Err: err})
		return unavailable(jmapserver.UpstreamAuthRequired, jmapserver.CodeUpstreamAuthRequired)
	case len(se.Rejected) > 0:
		addrs := make([]string, 0, len(se.Rejected))
		for a := range se.Rejected {
			addrs = append(addrs, a)
		}
		slices.Sort(addrs)
		e := newSetError("invalidRecipients", "The mail server refused some recipients.")
		e["invalidRecipients"] = addrs
		return e
	case se.Code == 552:
		return newSetError("tooLarge", "The message is larger than the mail server accepts.")
	case se.Temporary():
		return newSetError("rateLimit", "The mail server is not accepting mail right now; retry later.")
	case se.Code >= 500 && se.Stage == upstream.StageMail:
		return newSetError("forbiddenMailFrom", "The mail server does not allow this sender address.")
	case se.Code >= 500 && (se.Stage == upstream.StageData || se.Stage == upstream.StageRcpt):
		return newSetError("forbiddenToSend", "The mail server refused the message.")
	}
	return unavailable(jmapserver.UpstreamUnreachable, jmapserver.CodeUpstreamUnreachable)
}
