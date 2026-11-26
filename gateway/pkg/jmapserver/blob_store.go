package jmapserver

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UploadPrefix starts the id of every uploaded blob.
const UploadPrefix = 'u'

// UploadTTL is how long an upload survives without being referenced.
const UploadTTL = 24 * time.Hour

// ErrBlobNotFound is returned for unknown blobs, and for blobs of another user.
var ErrBlobNotFound = errors.New("blob not found")

var idEncoding = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

// UploadStore keeps uploaded blobs as files: <id> holds the data, <id>.json the
// owner and type. A file's modification time is its last use.
type UploadStore struct {
	dir string
}

// UploadMeta describes an uploaded blob.
type UploadMeta struct {
	UserID    string `json:"userId"`
	AccountID string `json:"accountId"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
}

// NewUploadStore creates dir if needed.
func NewUploadStore(dir string) (*UploadStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create blob dir: %w", err)
	}
	return &UploadStore{dir: dir}, nil
}

// newUploadID is "u" + 48-bit milliseconds + 80 random bits in sortable base32.
func newUploadID(now time.Time) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(b[6:])
	return string(UploadPrefix) + idEncoding.EncodeToString(b[:])
}

func validUploadID(id string) bool {
	if len(id) != 27 || id[0] != UploadPrefix {
		return false
	}
	for _, c := range id[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'v') {
			return false
		}
	}
	return true
}

// errUploadTooLarge is returned by save when the body exceeds MaxSizeUpload.
var errUploadTooLarge = errors.New("upload too large")

func (s *UploadStore) save(meta UploadMeta, body io.Reader) (string, UploadMeta, error) {
	id := newUploadID(time.Now())
	tmp, err := os.CreateTemp(s.dir, "upload-*.tmp")
	if err != nil {
		return "", meta, err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	n, err := io.Copy(tmp, io.LimitReader(body, MaxSizeUpload+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", meta, err
	}
	if n > MaxSizeUpload {
		return "", meta, errUploadTooLarge
	}
	meta.Size = n
	mj, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(s.dir, id+".json"), mj, 0o600); err != nil {
		return "", meta, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, id)); err != nil {
		os.Remove(filepath.Join(s.dir, id+".json"))
		return "", meta, err
	}
	return id, meta, nil
}

// Open returns an uploaded blob of the user's account. The caller closes the file.
func (s *UploadStore) Open(userID, accountID, blobID string) (*os.File, UploadMeta, error) {
	meta, err := s.meta(blobID)
	if err != nil {
		return nil, meta, err
	}
	if meta.UserID != userID || meta.AccountID != accountID {
		return nil, UploadMeta{}, ErrBlobNotFound
	}
	f, err := os.Open(filepath.Join(s.dir, blobID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, UploadMeta{}, ErrBlobNotFound
	}
	return f, meta, err
}

func (s *UploadStore) meta(blobID string) (UploadMeta, error) {
	var meta UploadMeta
	if !validUploadID(blobID) {
		return meta, ErrBlobNotFound
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, blobID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return meta, ErrBlobNotFound
	}
	if err != nil {
		return meta, err
	}
	return meta, json.Unmarshal(raw, &meta)
}

// Touch marks an upload as referenced (for example by a draft that has not been
// stored upstream yet), restarting its UploadTTL.
func (s *UploadStore) Touch(blobID string) error {
	if !validUploadID(blobID) {
		return ErrBlobNotFound
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(s.dir, blobID), now, now); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBlobNotFound
		}
		return err
	}
	return nil
}

// Cleanup removes uploads not used for UploadTTL, and temp files left by a crash.
// It returns how many blobs were removed.
func (s *UploadStore) Cleanup(now time.Time) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < UploadTTL {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".tmp"):
			os.Remove(filepath.Join(s.dir, name))
		case validUploadID(name):
			os.Remove(filepath.Join(s.dir, name+".json"))
			if os.Remove(filepath.Join(s.dir, name)) == nil {
				removed++
			}
		case strings.HasSuffix(name, ".json") && validUploadID(strings.TrimSuffix(name, ".json")):
			// Metadata whose data file is gone.
			if _, err := os.Stat(filepath.Join(s.dir, strings.TrimSuffix(name, ".json"))); errors.Is(err, os.ErrNotExist) {
				os.Remove(filepath.Join(s.dir, name))
			}
		}
	}
	return removed, nil
}
