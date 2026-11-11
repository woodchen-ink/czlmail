package syncer

import (
	"errors"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

func TestAdaptFileNodeWrite(t *testing.T) {
	create := map[string]any{
		"f": map[string]any{"parentId": "p", "name": "a.md", "blobId": "b1", "type": "text/markdown"},
		"d": map[string]any{"parentId": nil, "name": "dir"},
	}
	update := map[string]map[string]any{"x": {"blobId": "b2", "type": "application/json"}}

	// 旧写法原样发送。
	c, u := adaptFileNodeWrite(false, create, update)
	if c["f"].(map[string]any)["type"] != "text/markdown" || u["x"]["type"] != "application/json" {
		t.Fatalf("legacy changed: %v %v", c, u)
	}

	c, u = adaptFileNodeWrite(true, create, update)
	f, d := c["f"].(map[string]any), c["d"].(map[string]any)
	if f["@type"] != "File" || f["mediaType"] != "text/markdown" || f["type"] != nil {
		t.Fatalf("file: %v", f)
	}
	if d["@type"] != "Directory" || d["mediaType"] != nil {
		t.Fatalf("dir: %v", d)
	}
	// @type 不可变: 更新里只改名, 不补。
	if u["x"]["mediaType"] != "application/json" || u["x"]["type"] != nil || u["x"]["@type"] != nil {
		t.Fatalf("update: %v", u["x"])
	}
	// 调用方的对象不被改动, 换写法重试时还能从原样再改一次。
	if _, ok := create["f"].(map[string]any)["@type"]; ok || update["x"]["type"] != "application/json" {
		t.Fatal("input mutated")
	}
}

func TestFileNodeFromTypedSchema(t *testing.T) {
	file, err := FileNodeFrom([]byte(`{"id":"f","parentId":null,"@type":"File","blobId":"b","size":3,"name":"t.json","mediaType":"application/json"}`))
	if err != nil || file.IsDirectory || file.ContentType != "application/json" {
		t.Fatalf("file: %+v %v", file, err)
	}
	dir, err := FileNodeFrom([]byte(`{"id":"d","parentId":null,"@type":"Directory","name":"Notes"}`))
	if err != nil || !dir.IsDirectory {
		t.Fatalf("dir: %+v %v", dir, err)
	}
	legacy, err := FileNodeFrom([]byte(`{"id":"l","blobId":"b","name":"x.txt","type":"text/plain"}`))
	if err != nil || legacy.IsDirectory || legacy.ContentType != "text/plain" {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
}

func TestRejectsFileSchema(t *testing.T) {
	typed := &jmapx.SetError{Type: "invalidProperties", Description: "Invalid property.", Properties: []string{"type"}}
	if !rejectsFileSchema(typed) {
		t.Fatal("type rejection not recognised")
	}
	if rejectsFileSchema(&jmapx.SetError{Type: "invalidProperties", Properties: []string{"name"}}) {
		t.Fatal("unrelated property treated as schema mismatch")
	}
	if rejectsFileSchema(errors.New("x")) || rejectsFileSchema(nil) {
		t.Fatal("non set error")
	}
}
