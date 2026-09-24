// Package imgcache 是正文图片的磁盘缓存: 邮件里的远程图片与内嵌(cid)图片。
//
// 一张图一个文件, 文件名是键的 SHA-256; 首行存内容类型, 其后是原始字节。
// 按最近使用淘汰: 读命中时刷新修改时间, 总量超过上限时从最久没用的删起, 删到上限的 90%。
package imgcache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes 是默认容量上限。
const DefaultMaxBytes = 500 << 20

// Cache 是并发安全的磁盘缓存。
type Cache struct {
	dir      string
	maxBytes int64

	mu      sync.Mutex
	size    int64
	scanned bool
}

// New 在 dir 下建缓存。目录按需创建。
func New(dir string, maxBytes int64) *Cache {
	return &Cache{dir: dir, maxBytes: maxBytes}
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	// 两级目录, 免得单个目录里堆几万个文件。
	return filepath.Join(c.dir, name[:2], name)
}

// Get 返回缓存的内容与类型。
func (c *Cache) Get(key string) ([]byte, string, bool) {
	p := c.path(key)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, "", false
	}
	typ, data, ok := bytes.Cut(raw, []byte("\n"))
	if !ok {
		return nil, "", false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return data, string(typ), true
}

// Put 写入缓存, 超出容量时淘汰最久没用的。
func (c *Cache) Put(key, contentType string, data []byte) error {
	if strings.ContainsAny(contentType, "\r\n") {
		return errors.New("invalid content type")
	}
	// 先统计再写: 首次统计放在写入之后, 新文件会被算两遍。
	c.mu.Lock()
	c.scan()
	c.mu.Unlock()

	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var old int64
	if info, err := os.Stat(p); err == nil {
		old = info.Size()
	}
	// 先写临时文件再改名: 写到一半的文件不会被当成完整的图片读出去。
	tmp, err := os.CreateTemp(filepath.Dir(p), "*.part")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	w.WriteString(contentType)
	w.WriteByte('\n')
	w.Write(data)
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.size += int64(len(contentType)+1+len(data)) - old
	if c.size > c.maxBytes {
		c.evict()
	}
	return nil
}

// Size 返回缓存占用的字节数。
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scanned = false
	c.scan()
	return c.size
}

// Clear 删除全部缓存。
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := os.RemoveAll(c.dir)
	c.size = 0
	c.scanned = true
	if err != nil {
		// 个别文件被占用时不算失败, 重新统计剩下多少。
		c.scanned = false
		c.scan()
	}
	return err
}

type entry struct {
	path string
	size int64
	mod  time.Time
}

func (c *Cache) walk() []entry {
	var out []entry
	filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out = append(out, entry{p, info.Size(), info.ModTime()})
		}
		return nil
	})
	return out
}

// scan 在首次使用时统计总量。调用方持锁。
func (c *Cache) scan() {
	if c.scanned {
		return
	}
	c.size = 0
	for _, e := range c.walk() {
		c.size += e.size
	}
	c.scanned = true
}

// evict 删到上限的 90%, 省得每写一张就淘汰一次。调用方持锁。
func (c *Cache) evict() {
	entries := c.walk()
	slices.SortFunc(entries, func(a, b entry) int { return a.mod.Compare(b.mod) })
	var total int64
	for _, e := range entries {
		total += e.size
	}
	target := c.maxBytes / 10 * 9
	for _, e := range entries {
		if total <= target {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
	c.size = total
}
