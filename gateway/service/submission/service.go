// Package submission implements sending for upstream accounts: EmailSubmission
// (RFC 8621 §7) and Identity (§6), with the gateway's rules on top: one Sent copy
// under the draft's Email id, sender checks against the account's identities, a
// duplicate-submit guard, and SMTP replies mapped to JMAP errors (the server's
// own words only reach logs and the admin).
package submission

import (
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Retention and limits.
const (
	// SubmissionRetention: EmailSubmission records are destroyed after a week.
	SubmissionRetention = 7 * 24 * time.Hour
	// DuplicateWindow: the same draft content submitted again within it returns
	// the first submission instead of sending twice.
	DuplicateWindow = 10 * time.Minute
	// ChangeRetention matches the mail changelog window.
	ChangeRetention = 30 * 24 * time.Hour
	// MaxRecipients per submission.
	MaxRecipients = 500
	// MaxMessageSize is the largest draft fetched for sending.
	MaxMessageSize = jmapserver.MaxSizeUpload
)

// Options tune a Service; the zero value is production.
type Options struct {
	// SentCopyWait overrides upstream.SentCopyWait (tests).
	SentCopyWait time.Duration
	// RetryDelays are the waits before retrying a temporary SMTP failure
	// (default 2 s, then 5 s: two retries).
	RetryDelays []time.Duration
}

// Service serves the Submission methods.
type Service struct {
	db         *gorm.DB
	mgr        *service.UpstreamManager
	log        *slog.Logger
	opts       Options
	changes    changeLog
	Identities *Identities

	mu      sync.Mutex
	pending map[string]*keyLock // duplicate guard, by (account, email, hash)
}

// New returns the service.
func New(db *gorm.DB, mgr *service.UpstreamManager, log *slog.Logger, opts Options) *Service {
	if opts.RetryDelays == nil {
		opts.RetryDelays = []time.Duration{2 * time.Second, 5 * time.Second}
	}
	s := &Service{db: db, mgr: mgr, log: log, opts: opts, changes: changeLog{db: db}, pending: map[string]*keyLock{}}
	s.Identities = newIdentities(db, log, s.identitySource, func(accountID string) { s.publish(accountID, TypeIdentity) })
	return s
}

// Register adds the methods to the JMAP server.
func (s *Service) Register(j *jmapserver.Server) {
	cap := service.SubmissionCapability
	j.Register("Identity/get", cap, s.identityGet)
	j.Register("Identity/changes", cap, s.identityChanges)
	j.Register("Identity/set", cap, s.identitySet)
	j.Register("EmailSubmission/get", cap, s.submissionGet)
	j.Register("EmailSubmission/changes", cap, s.submissionChanges)
	j.Register("EmailSubmission/query", cap, s.submissionQuery)
	j.Register("EmailSubmission/set", cap, s.submissionSet)
}

// identitySource returns the running upstream of accountID when it lists its
// own identities (upstream.IdentitySource), else nil.
func (s *Service) identitySource(accountID string) upstream.IdentitySource {
	if s.mgr == nil {
		return nil
	}
	h, _ := s.mgr.Lookup(accountID)
	if h == nil {
		return nil
	}
	src, _ := h.Upstream.(upstream.IdentitySource)
	return src
}

// publish pushes the current state of a gateway-kept type.
func (s *Service) publish(accountID, typ string) {
	st, err := s.changes.state(s.db, accountID, typ)
	if err == nil {
		s.mgr.Publish(accountID, map[string]string{typ: st})
	}
}

// Prune destroys submissions older than SubmissionRetention and trims the
// changelog (cron).
func (s *Service) Prune() (int64, error) {
	cut := time.Now().UTC().Add(-SubmissionRetention)
	var old []model.EmailSubmission
	if err := s.db.Select("id", "public_id", "account_id").Where("created_at < ?", cut).Find(&old).Error; err != nil {
		return 0, err
	}
	accounts := map[string]bool{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, o := range old {
			if err := tx.Delete(&model.EmailSubmission{}, o.ID).Error; err != nil {
				return err
			}
			if err := s.changes.record(tx, o.AccountID, TypeEmailSubmission, o.PublicID, kindDestroyed); err != nil {
				return err
			}
			accounts[o.AccountID] = true
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for a := range accounts {
		s.publish(a, TypeEmailSubmission)
	}
	n, err := s.changes.prune(ChangeRetention)
	return int64(len(old)) + n, err
}

// lockKey serialises submissions of the same content (the duplicate guard).
func (s *Service) lockKey(key string) func() {
	s.mu.Lock()
	k := s.pending[key]
	if k == nil {
		k = &keyLock{}
		s.pending[key] = k
	}
	k.refs++
	s.mu.Unlock()
	k.mu.Lock()
	return func() {
		k.mu.Unlock()
		s.mu.Lock()
		if k.refs--; k.refs == 0 {
			delete(s.pending, key)
		}
		s.mu.Unlock()
	}
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}
