package mail

import (
	"container/list"
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
type Cache struct {
	dir string
	max int64

	mu    sync.Mutex
	size  int64
	lru   *list.List // of *cacheEntry, front = most recent
	byKey map[string]*list.Element
	// fills deduplicates concurrent fetches of the same key.
	fills map[string]*fill
}

type cacheEntry struct {
	key  string
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
	c := &Cache{dir: dir, max: maxBytes, lru: list.New(), byKey: map[string]*list.Element{}, fills: map[string]*fill{}}
	type old struct {
		key  string
		size int64
		mod  time.Time
	}
	var found []old
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tmp") {
			os.Remove(path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		found = append(found, old{key: filepath.ToSlash(rel), size: info.Size(), mod: info.ModTime()})
		return nil
	})
	slices.SortFunc(found, func(a, b old) int { return a.mod.Compare(b.mod) })
	for _, f := range found {
		c.byKey[f.key] = c.lru.PushFront(&cacheEntry{key: f.key, size: f.size})
		c.size += f.size
	}
	c.mu.Lock()
	c.evict()
	c.mu.Unlock()
	return c, nil
}

// cacheKey names an email section's file: <account>/<hash of email and section>.
func cacheKey(accountID, emailID, section string) string {
	h := sha256.Sum256([]byte(emailID + "\x00" + section))
	return accountID + "/" + hex.EncodeToString(h[:16])
}

func (c *Cache) path(key string) string { return filepath.Join(c.dir, filepath.FromSlash(key)) }

// Peek opens key when it is cached, without fetching.
func (c *Cache) Peek(key string) (*File, bool) {
	c.mu.Lock()
	el := c.byKey[key]
	if el != nil {
		c.lru.MoveToFront(el)
	}
	c.mu.Unlock()
	if el == nil {
		return nil, false
	}
	f, err := os.Open(c.path(key))
	if err != nil {
		return nil, false
	}
	return &File{File: f}, true
}

// Open returns the cached file of key, running fetch to fill it first when it
// is missing. The caller closes the file.
func (c *Cache) Open(key string, fetch func(w io.Writer) error) (*File, error) {
	for {
		c.mu.Lock()
		if el := c.byKey[key]; el != nil {
			c.lru.MoveToFront(el)
			c.mu.Unlock()
			f, err := os.Open(c.path(key))
			if err == nil {
				return &File{File: f}, nil
			}
			c.mu.Lock()
			c.remove(key) // deleted behind our back
			c.mu.Unlock()
			continue
		}
		if fl := c.fills[key]; fl != nil {
			c.mu.Unlock()
			<-fl.done
			if fl.err != nil {
				return nil, fl.err
			}
			continue
		}
		fl := &fill{done: make(chan struct{})}
		c.fills[key] = fl
		c.mu.Unlock()

		f, err := c.fill(key, fetch, true)
		c.mu.Lock()
		delete(c.fills, key)
		c.mu.Unlock()
		fl.err = err
		close(fl.done)
		return f, err
	}
}

func (c *Cache) fill(key string, fetch func(w io.Writer) error, keep bool) (*File, error) {
	path := c.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "fill-*.tmp")
	if err != nil {
		return nil, err
	}
	err = fetch(tmp)
	size, _ := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if !keep || c.max <= 0 || size > c.max/4 {
		// Too big to keep (or no cache): served once, deleted on close.
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, err
		}
		return tempFile(tmp), nil
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	c.mu.Lock()
	c.byKey[key] = c.lru.PushFront(&cacheEntry{key: key, size: size})
	c.size += size
	c.evict()
	c.mu.Unlock()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		// Evicted at once by a concurrent fill: fetch again without keeping it.
		return c.fill(key, fetch, false)
	}
	if err != nil {
		return nil, err
	}
	return &File{File: f}, nil
}

// evict drops least recently used entries until the cache fits. c.mu is held.
func (c *Cache) evict() {
	for c.size > c.max && c.lru.Len() > 0 {
		e := c.lru.Back().Value.(*cacheEntry)
		c.remove(e.key)
	}
}

func (c *Cache) remove(key string) {
	el := c.byKey[key]
	if el == nil {
		return
	}
	e := el.Value.(*cacheEntry)
	c.lru.Remove(el)
	delete(c.byKey, key)
	c.size -= e.size
	os.Remove(c.path(key))
}

// DropAccount removes every entry of an account (its upstream was deleted).
func (c *Cache) DropAccount(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := accountID + "/"
	for key := range c.byKey {
		if strings.HasPrefix(key, prefix) {
			c.remove(key)
		}
	}
	os.RemoveAll(filepath.Join(c.dir, accountID))
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

// tempFile wraps an unkept fill so closing it deletes the file.
func tempFile(f *os.File) *File { return &File{File: f, temp: true} }
