package notes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Service serializes local edits and synchronization; network work uses bounded contexts.
// The dedicated FULL-synchronous database stores original notes, not disposable cache data.
type Service struct {
	mu      sync.Mutex
	access  sync.RWMutex
	syncMu  sync.Mutex
	db      *sql.DB
	root    string
	config  Config
	vaults  map[string]Vault
	entries map[string]Entry
	remote  Remote
	changed func()
}

// Open restores durable local overlays without connecting to a remote account.
func Open(root string, changed func()) (*Service, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, ErrStorage
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "notes.db")+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, ErrStorage
	}
	db.SetMaxOpenConns(1)
	s := &Service{db: db, root: root, config: Config{Enabled: true, AllowRead: true}, vaults: map[string]Vault{}, entries: map[string]Entry{}, changed: changed}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS records (kind TEXT NOT NULL,id TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(kind,id)); CREATE TABLE IF NOT EXISTS requests (key TEXT PRIMARY KEY, digest TEXT NOT NULL, result TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, ErrStorage
	}
	rows, err := db.Query(`SELECT kind,id,value FROM records`)
	if err != nil {
		db.Close()
		return nil, ErrStorage
	}
	for rows.Next() {
		var kind, id, value string
		if err = rows.Scan(&kind, &id, &value); err != nil {
			break
		}
		switch kind {
		case "config":
			err = json.Unmarshal([]byte(value), &s.config)
		case "vault":
			var v Vault
			err = json.Unmarshal([]byte(value), &v)
			s.vaults[id] = v
		case "entry":
			var e Entry
			err = json.Unmarshal([]byte(value), &e)
			s.entries[id] = e
		}
		if err != nil {
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		db.Close()
		return nil, ErrStorage
	}
	return s, nil
}

// Close releases the durable store after the worker has stopped.
func (s *Service) Close() error { s.mu.Lock(); defer s.mu.Unlock(); return s.db.Close() }

// SetRemote swaps a session-scoped transport; nil preserves fully offline editing.
func (s *Service) SetRemote(r Remote) { s.mu.Lock(); s.remote = r; s.mu.Unlock() }

// GetConfig reads the device-local module and MCP gates.
func (s *Service) GetConfig() Config { s.mu.Lock(); defer s.mu.Unlock(); return s.config }

// AcquireMCP pins permission until this call finishes; disabling waits for active calls to drain.
func (s *Service) AcquireMCP(write bool) (func(), error) {
	s.access.RLock()
	c := s.GetConfig()
	if !c.Enabled {
		s.access.RUnlock()
		return nil, ErrDisabled
	}
	if !c.AllowRead || write && !c.AllowWrite {
		s.access.RUnlock()
		return nil, ErrForbidden
	}
	return s.access.RUnlock, nil
}

// Configure persists the gate before notifying UI; previously saved data is untouched.
func (s *Service) Configure(c Config) error {
	s.access.Lock()
	defer s.access.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.put("config", "settings", c); err != nil {
		return err
	}
	s.config = c
	s.notify()
	return nil
}

// notify signals a version change without sending any note content through the event bus.
func (s *Service) notify() {
	if s.changed != nil {
		s.changed()
	}
}

// put atomically persists one record; callers update their memory snapshot only on success.
func (s *Service) put(kind, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrStorage
	}
	if _, err = s.db.Exec(`INSERT INTO records(kind,id,value) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET value=excluded.value`, kind, id, string(data)); err != nil {
		return ErrStorage
	}
	return nil
}

// commit saves the edit and its optional idempotent response in one database transaction.
func (s *Service) commit(e Entry, key, digest string) error {
	data, err := json.Marshal(e)
	if err != nil {
		return ErrStorage
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO records(kind,id,value) VALUES('entry',?,?) ON CONFLICT(kind,id) DO UPDATE SET value=excluded.value`, e.ID, string(data)); err != nil {
		return ErrStorage
	}
	if key != "" {
		if _, err = tx.Exec(`INSERT INTO requests(key,digest,result) VALUES(?,?,?)`, key, digest, string(data)); err != nil {
			return ErrStorage
		}
	}
	if err = tx.Commit(); err != nil {
		return ErrStorage
	}
	s.entries[e.ID] = e
	s.notify()
	return nil
}

// retry returns the original accepted response; a reused key cannot mutate a new payload.
func (s *Service) retry(key, digest string) (Entry, bool, error) {
	if key == "" {
		return Entry{}, false, nil
	}
	var hash, data string
	err := s.db.QueryRow(`SELECT digest,result FROM requests WHERE key=?`, key).Scan(&hash, &data)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, ErrStorage
	}
	if hash != digest {
		return Entry{}, false, ErrInvalid
	}
	var e Entry
	if json.Unmarshal([]byte(data), &e) != nil {
		return Entry{}, false, ErrStorage
	}
	return e, true, nil
}

// newID creates stable local identities independent of filename and remote object IDs.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// digest binds an idempotency key to one exact request, including its operation name.
func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// clone removes mutable pointer aliases before returning state outside the service lock.
func clone(e Entry) Entry {
	if e.Base != nil {
		n := *e.Base
		e.Base = &n
	}
	if e.Remote != nil {
		n := *e.Remote
		e.Remote = &n
	}
	return e
}
