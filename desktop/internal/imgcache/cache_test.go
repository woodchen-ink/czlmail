package imgcache

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestPutGet(t *testing.T) {
	c := New(t.TempDir(), 1<<20)
	if _, _, ok := c.Get("a"); ok {
		t.Fatal("empty cache hit")
	}
	data := []byte("\x89PNG\r\n\x1a\nrest\nwith newline")
	if err := c.Put("a", "image/png", data); err != nil {
		t.Fatal(err)
	}
	got, typ, ok := c.Get("a")
	if !ok || typ != "image/png" || !bytes.Equal(got, data) {
		t.Fatalf("got %q %q %v", got, typ, ok)
	}
	if c.Size() != int64(len("image/png")+1+len(data)) {
		t.Errorf("size = %d", c.Size())
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.Get("a"); ok || c.Size() != 0 {
		t.Error("clear left data behind")
	}
}

func TestEvictOldest(t *testing.T) {
	c := New(t.TempDir(), 1000)
	blob := bytes.Repeat([]byte("x"), 300)
	old := time.Now().Add(-time.Hour)
	for i, k := range []string{"k1", "k2", "k3"} {
		if err := c.Put(k, "image/gif", blob); err != nil {
			t.Fatal(err)
		}
		// 按写入顺序排出新旧, k1 最旧。
		ts := old.Add(time.Duration(i) * time.Minute)
		os.Chtimes(c.path(k), ts, ts)
	}
	// 读一次 k1, 它就变成最新的。
	c.Get("k1")
	if err := c.Put("k4", "image/gif", blob); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.Get("k2"); ok {
		t.Error("k2 should be evicted first")
	}
	if _, _, ok := c.Get("k1"); !ok {
		t.Error("recently read k1 was evicted")
	}
	if _, _, ok := c.Get("k4"); !ok {
		t.Error("new entry was evicted")
	}
	if c.Size() > 900 {
		t.Errorf("size %d over target", c.Size())
	}
}
