package mail

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultCacheBytes bounds the on-disk cache of fetched messages and parts.
const DefaultCacheBytes = 500 << 20

// Cache keeps fetched message sections on disk, least recently used first out.
// Emails never change content and their ids are never reused, so an entry
// never goes stale; entries of destroyed emails simply age out.
//
// Every fill writes a file of its own (<key>~<random>), so a file a reader
// still has open never blocks the next fill of its key. On Windows an open
// file cannot be deleted: such files wait in doomed, still counted in size,
// and are retried on every eviction.
type Cache struct {
	dir string
	max int64

	mu     sync.Mutex
	size   int64
	lru    *list.List // of *cacheEntry, front = most recent
	byKey  map[string]*list.Element
	doomed map[string]int64 // file path → size, deletion failed
	// fills deduplicates concurrent fetches of the same key.
	fills map[string]*fill
}

type cacheEntry struct {
	key  string
	path string
	size int64
}

type fill struct {
	done chan struct{}
	err  error
}

// NewCache opens the cache in dir, keeping what an earlier run left (oldest
// first out) within maxBytes. maxBytes <= 0 keeps nothing: every entry is
// dropped as soon as it is closed.
func NewCache(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	c := &Cache{dir: dir, max: maxBytes, lru: list.New(), byKey: map[string]*list.Element{},
		doomed: map[string]int64{}, fills: map[string]*fill{}}
	type old struct {
		key, path string
		size      int64
		mod       time.Time
	}
	var found []old
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		key, _, ok := strings.Cut(filepath.ToSlash(rel), "~")
		info, ierr := d.Info()
		if !ok || strings.HasSuffix(path, ".tmp") || ierr != nil {
			os.Remove(path) // a crashed fill, or a file of an older layout
			return nil
		}
		found = append(found, old{key: key, path: path, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	slices.SortFunc(found, func(a, b old) int { return a.mod.Compare(b.mod) })
	for _, f := range found {
		if el := c.byKey[f.key]; el != nil {
			c.drop(el) // an older copy of the same key
		}
		c.byKey[f.key] = c.lru.PushFront(&cacheEntry{key: f.key, path: f.path, size: f.size})
		c.size += f.size
	}
	c.mu.Lock()
	c.evict()
	c.mu.Unlock()
	return c, nil
}

// cacheKey names an email section's entry: <account>/<hash of email and section>.
func cacheKey(accountID, emailID, section string) string {
	h := sha256.Sum256([]byte(emailID + "\x00" + section))
	return accountID + "/" + hex.EncodeToString(h[:16])
}

// Peek opens key when it is cached, without fetching.
func (c *Cache) Peek(key string) (*File, bool) {
	c.mu.Lock()
	el := c.byKey[key]
	var path string
	if el != nil {
		c.lru.MoveToFront(el)
		path = el.Value.(*cacheEntry).path
	}
	c.mu.Unlock()
	if el == nil {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	return &File{File: f}, true
}

// Open returns the cached file of key, running fetch to fill it first when it
// is missing. Concurrent callers of one key share one fetch; a caller whose
// own context is still live fetches again when the shared fetch was canceled.
// The caller closes the file.
func (c *Cache) Open(ctx context.Context, key string, fetch func(w io.Writer) error) (*File, error) {
	for {
		c.mu.Lock()
		if el := c.byKey[key]; el != nil {
			c.lru.MoveToFront(el)
			path := el.Value.(*cacheEntry).path
			c.mu.Unlock()
			f, err := os.Open(path)
			if err == nil {
				return &File{File: f}, nil
			}
			c.mu.Lock()
			if c.byKey[key] == el {
				c.drop(el) // deleted behind our back
			}
			c.mu.Unlock()
			continue
		}
		if fl := c.fills[key]; fl != nil {
			c.mu.Unlock()
			select {
			case <-fl.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if fl.err != nil {
				if isContextErr(fl.err) && ctx.Err() == nil {
					continue
				}
				return nil, fl.err
			}
			continue
		}
		fl := &fill{done: make(chan struct{})}
		c.fills[key] = fl
		c.mu.Unlock()

		f, err := c.fill(key, fetch)
		c.mu.Lock()
		delete(c.fills, key)
		c.mu.Unlock()
		fl.err = err
		close(fl.done)
		return f, err
	}
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Cache) fill(key string, fetch func(w io.Writer) error) (*File, error) {
	base := filepath.Join(c.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(base), "fill-*.tmp")
	if err != nil {
		return nil, err
	}
	err = fetch(tmp)
	size, _ := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if c.max <= 0 || size > c.max/4 {
		// Too big to keep (or no cache): served once, deleted on close.
		return &File{File: tmp, temp: true}, nil
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	path := base + "~" + randomHex(4)
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	// Open before the entry is visible to eviction, so the caller always gets it.
	f, err := os.Open(path)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	c.mu.Lock()
	if el := c.byKey[key]; el != nil {
		c.drop(el)
	}
	c.byKey[key] = c.lru.PushFront(&cacheEntry{key: key, path: path, size: size})
	c.size += size
	c.evict()
	c.mu.Unlock()
	return &File{File: f}, nil
}

// evict retries doomed files and drops least recently used entries until the
// cache fits. c.mu is held.
func (c *Cache) evict() {
	for path, n := range c.doomed {
		if err := os.Remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
			delete(c.doomed, path)
			c.size -= n
		}
	}
	for c.size > c.max && c.lru.Len() > 0 {
		c.drop(c.lru.Back())
	}
}

// drop removes an entry; its file goes now or, while open on Windows, later.
func (c *Cache) drop(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.lru.Remove(el)
	if c.byKey[e.key] == el {
		delete(c.byKey, e.key)
	}
	if err := os.Remove(e.path); err == nil || errors.Is(err, os.ErrNotExist) {
		c.size -= e.size
	} else {
		c.doomed[e.path] = e.size
	}
}

// DropAccount removes every entry of an account (its upstream was deleted).
func (c *Cache) DropAccount(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := accountID + "/"
	for key, el := range c.byKey {
		if strings.HasPrefix(key, prefix) {
			c.drop(el)
		}
	}
	_ = os.RemoveAll(filepath.Join(c.dir, accountID)) // leaves only files still open
}

// File is an open cache entry.
type File struct {
	*os.File
	temp bool
}

// Close closes the file; an entry that was not kept is deleted.
func (f *File) Close() error {
	err := f.File.Close()
	if f.temp {
		os.Remove(f.Name())
	}
	return err
}
