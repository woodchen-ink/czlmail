// Package mailindex is one upstream's index and changelog, in its own SQLite file.
//
// The sync engine writes through Store.Write: one transaction holds the index rows,
// the upstream sync point (UIDVALIDITY / UIDNEXT / HIGHESTMODSEQ) and the changelog
// entries, so the three never disagree. JMAP methods read with Store.DB and compute
// /changes with Store.Changes. States are "<generation>-<seq>" per type.
package mailindex

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/model"
)

// Object types of the changelog and states.
const (
	TypeMailbox = "Mailbox"
	TypeEmail   = "Email"
	TypeThread  = "Thread"
)

// Types lists every type with a state, in a fixed order.
var Types = []string{TypeMailbox, TypeEmail, TypeThread}

// Change kinds. KindCounts is a Mailbox update that only changed counts.
const (
	KindCreated   = "created"
	KindUpdated   = "updated"
	KindCounts    = "counts"
	KindDestroyed = "destroyed"
)

// ErrNotReady means the first import has not finished: there is no state yet.
var ErrNotReady = errors.New("mailindex: first sync not finished")

// Store is one upstream's index.
type Store struct {
	db   *gorm.DB
	path string

	wmu         sync.Mutex // one writer at a time
	mu          sync.RWMutex
	meta        model.IndexMeta
	onDelivered func(emailIDs []string)
	onCommit    func(states map[string]string)
}

// OnCommit sets fn to run after every commit that changed a state, with the
// new state of each changed type: sync rounds and gateway writes alike, so
// push never depends on a caller remembering to publish. Like OnDelivered it
// runs under the write lock and must not write to the index.
func (s *Store) OnCommit(fn func(states map[string]string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCommit = fn
}

// OnDelivered sets fn to run after each commit that added emails the sync engine
// found new on the server (see Tx.Delivered). It runs on the writer's goroutine
// while the write lock is held, so it must not write to the index; reading
// through DB is fine.
func (s *Store) OnDelivered(fn func(emailIDs []string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDelivered = fn
}

// Open opens (creating and migrating) the index of accountID.
func Open(dataDir, accountID string) (*Store, error) {
	path, err := database.UpstreamPath(dataDir, accountID)
	if err != nil {
		return nil, err
	}
	db, err := database.OpenUpstream(dataDir, accountID)
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&model.IndexMeta{}, &model.IndexMailbox{}, &model.IndexEmail{}, &model.IndexMembership{}, &model.IndexEmailRef{}, &model.IndexChange{}); err != nil {
		database.Close(db)
		return nil, fmt.Errorf("migrate index: %w", err)
	}
	s := &Store{db: db, path: path}
	err = db.Where(model.IndexMeta{ID: 1}).Attrs(model.IndexMeta{Generation: time.Now().Unix()}).FirstOrCreate(&s.meta).Error
	if err != nil {
		database.Close(db)
		return nil, err
	}
	return s, nil
}

// Remove deletes the index files of accountID (database plus WAL and SHM).
// The store must be closed.
func Remove(dataDir, accountID string) error {
	path, err := database.UpstreamPath(dataDir, accountID)
	if err != nil {
		return err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return database.Close(s.db) }

// DB is the index database for reads. Writes go through Write only.
func (s *Store) DB() *gorm.DB { return s.db }

// Meta returns a copy of the index metadata.
func (s *Store) Meta() model.IndexMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta
}

// Ready reports whether the first import finished.
func (s *Store) Ready() bool { return s.Meta().Ready }

// State returns the current state of a type, or ErrNotReady.
func (s *Store) State(typ string) (string, error) {
	m := s.Meta()
	if !m.Ready {
		return "", ErrNotReady
	}
	return formatState(m.Generation, typeSeq(&m, typ)), nil
}

// States returns the state of every type, or nil before the first import finished.
func (s *Store) States() map[string]string {
	m := s.Meta()
	if !m.Ready {
		return nil
	}
	out := make(map[string]string, len(Types))
	for _, t := range Types {
		out[t] = formatState(m.Generation, typeSeq(&m, t))
	}
	return out
}

func typeSeq(m *model.IndexMeta, typ string) int64 {
	switch typ {
	case TypeMailbox:
		return m.MailboxSeq
	case TypeEmail:
		return m.EmailSeq
	case TypeThread:
		return m.ThreadSeq
	}
	return 0
}

func formatState(gen, seq int64) string {
	return strconv.FormatInt(gen, 10) + "-" + strconv.FormatInt(seq, 10)
}

// ParseState splits a state string; ok is false when it is malformed.
func ParseState(state string) (gen, seq int64, ok bool) {
	g, q, found := strings.Cut(state, "-")
	if !found {
		return 0, 0, false
	}
	gen, err1 := strconv.ParseInt(g, 10, 64)
	seq, err2 := strconv.ParseInt(q, 10, 64)
	if err1 != nil || err2 != nil || seq < 0 {
		return 0, 0, false
	}
	return gen, seq, true
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns prefix + 16 random base32 characters (80 bits).
func newID(prefix byte) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return string(prefix) + idEncoding.EncodeToString(b[:])
}

// NewMailboxID, NewEmailID and NewThreadID allocate public JMAP ids.
func NewMailboxID() string { return newID('m') }
func NewEmailID() string   { return newID('e') }
func NewThreadID() string  { return newID('t') }
