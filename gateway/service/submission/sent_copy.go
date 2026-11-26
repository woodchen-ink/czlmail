package submission

import (
	"context"
	"errors"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Sent-copy errors.
var (
	ErrInvalidSentCopy    = errors.New("invalid sent copy strategy")
	ErrUpstreamNotRunning = errors.New("upstream not running")
	ErrCannotTestSend     = errors.New("upstream cannot run a test send")
)

// SentCopyTest is the outcome of a test send. On failure Code, Msg and Detail
// follow the connection-test scheme (Detail: the server's words, admin only).
type SentCopyTest struct {
	OK       bool   `json:"ok"`
	SentCopy string `json:"sentCopy"` // the strategy now stored
	// ServerSaves is what the test saw: the server kept its own copy in Sent.
	ServerSaves bool   `json:"serverSaves"`
	WaitedMs    int64  `json:"waitedMs"`
	Code        int    `json:"code,omitempty"`
	Msg         string `json:"msg,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// SetSentCopy stores an upstream's sent-copy strategy.
func (s *Service) SetSentCopy(accountID, mode string) error {
	switch mode {
	case upstream.SentCopyAuto, upstream.SentCopyGateway, upstream.SentCopyServer:
	default:
		return ErrInvalidSentCopy
	}
	return s.db.Model(&model.Upstream{}).Where("public_id = ?", accountID).Update("sent_copy", mode).Error
}

// TestSentCopy sends a message to the upstream's own address, watches Sent for
// the server's copy and stores the strategy that matches.
func (s *Service) TestSentCopy(ctx context.Context, accountID string) (*SentCopyTest, error) {
	h, uerr := s.mgr.Lookup(accountID)
	if h == nil || (uerr != nil && uerr.Status != jmapserver.UpstreamUnreachable) {
		return nil, ErrUpstreamNotRunning
	}
	tester, ok := h.Upstream.(upstream.SentCopyTester)
	if !ok {
		return nil, ErrCannotTestSend
	}
	wait := s.opts.SentCopyWait
	if wait <= 0 {
		wait = upstream.SentCopyWait
	}
	start := time.Now()
	saves, err := tester.TestSentCopy(ctx, wait)
	res := &SentCopyTest{WaitedMs: time.Since(start).Milliseconds()}
	if err != nil {
		f := upstream.ProbeFailure(err)
		var se *upstream.SendError
		if errors.As(err, &se) && se.Reply != "" {
			f.Detail = se.Reply
		}
		res.Code, res.Msg, res.Detail = f.Code, f.Msg, f.Detail
		var row model.Upstream
		if s.db.Select("sent_copy").Where("public_id = ?", accountID).First(&row).Error == nil {
			res.SentCopy = row.SentCopy
		}
		return res, nil
	}
	res.OK, res.ServerSaves = true, saves
	res.SentCopy = upstream.SentCopyGateway
	if saves {
		res.SentCopy = upstream.SentCopyServer
	}
	if err := s.SetSentCopy(accountID, res.SentCopy); err != nil {
		return nil, err
	}
	return res, nil
}
