package jmapserver_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

func TestUploadDownloadThroughGoJMAP(t *testing.T) {
	e := newEnv(t, 0)
	c := e.client("alice")
	data := []byte("hello blob \x00\x01")
	up, err := c.Upload("a1", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if up.Account != "a1" || up.Size != uint64(len(data)) || !strings.HasPrefix(string(up.ID), "u") {
		t.Fatalf("upload response: %+v", up)
	}
	rc, err := c.Download("a1", up.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("downloaded %q", got)
	}

	// Download headers: type from the query, attachment with an encoded name.
	resp, err := c.HttpClient.Get(e.ts.URL + "/jmap/download/a1/" + string(up.ID) + "/%E6%8A%A5%E5%91%8A.txt?type=text/plain")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/plain" ||
		resp.Header.Get("Content-Disposition") != "attachment; filename*=utf-8''%E6%8A%A5%E5%91%8A.txt" {
		t.Fatalf("headers: %v", resp.Header)
	}

	// The blob belongs to alice's a1: another account of hers does not see it.
	if status, _, _ := e.get("/jmap/download/a2/"+string(up.ID)+"/x", "alice"); status != http.StatusNotFound {
		t.Fatalf("other account: %d", status)
	}
	// Unknown account.
	if status, _, body := e.get("/jmap/download/zz/"+string(up.ID)+"/x", "alice"); status != http.StatusNotFound ||
		problemOf(t, body)["code"] != 40401.0 {
		t.Fatalf("unknown account: %d %s", status, body)
	}
}

func TestUploadTooLarge(t *testing.T) {
	e := newEnv(t, 0)
	req, _ := http.NewRequest("POST", e.ts.URL+"/jmap/upload/a1/", io.LimitReader(zeros{}, jmapserver.MaxSizeUpload+1))
	req.SetBasicAuth("alice", "pwalice")
	status, _, body := e.do(req)
	if status != http.StatusRequestEntityTooLarge || problemOf(t, body)["limit"] != "maxSizeUpload" {
		t.Fatalf("%d %s", status, body)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type unavailableSource struct{}

func (unavailableSource) OpenBlob(_ context.Context, _, _, blobID string) (*jmapserver.Blob, error) {
	if blobID == "m404" {
		return nil, jmapserver.ErrBlobNotFound
	}
	return nil, &jmapserver.UpstreamError{Status: jmapserver.UpstreamAuthRequired, Code: 40110}
}

type staticSource struct{}

func (staticSource) OpenBlob(context.Context, string, string, string) (*jmapserver.Blob, error) {
	return &jmapserver.Blob{Body: io.NopCloser(strings.NewReader("raw message")), Size: 11}, nil
}

func TestDownloadDispatchByPrefix(t *testing.T) {
	e := newEnv(t, 0)
	e.srv.RegisterBlobSource('m', unavailableSource{})
	e.srv.RegisterBlobSource('p', staticSource{})

	status, ctype, body := e.get("/jmap/download/a1/m123/x.eml", "alice")
	if status != http.StatusServiceUnavailable || ctype != "application/problem+json" {
		t.Fatalf("%d %s %s", status, ctype, body)
	}
	p := problemOf(t, body)
	up, _ := p["czlUpstream"].(map[string]any)
	if p["code"] != 50301.0 || up["status"] != "authRequired" || up["code"] != 40110.0 {
		t.Fatalf("problem: %v", p)
	}
	if status, _, _ := e.get("/jmap/download/a1/m404/x", "alice"); status != http.StatusNotFound {
		t.Fatalf("not found: %d", status)
	}
	if status, _, body := e.get("/jmap/download/a1/p1.2/x", "alice"); status != 200 || string(body) != "raw message" {
		t.Fatalf("static: %d %s", status, body)
	}
	if status, _, _ := e.get("/jmap/download/a1/zzz/x", "alice"); status != http.StatusNotFound {
		t.Fatalf("unknown prefix: %d", status)
	}
}

func TestUploadCleanup(t *testing.T) {
	e := newEnv(t, 0)
	c := e.client("alice")
	old, err := c.Upload("a1", strings.NewReader("old"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := c.Upload("a1", strings.NewReader("fresh"))
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-jmapserver.UploadTTL - time.Minute)
	if err := os.Chtimes(filepath.Join(e.dataDir, "blobs", string(old.ID)), past, past); err != nil {
		t.Fatal(err)
	}
	n, err := e.srv.Uploads().Cleanup(time.Now())
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if status, _, _ := e.get("/jmap/download/a1/"+string(old.ID)+"/x", "alice"); status != http.StatusNotFound {
		t.Fatalf("expired blob: %d", status)
	}
	if status, _, _ := e.get("/jmap/download/a1/"+string(fresh.ID)+"/x", "alice"); status != 200 {
		t.Fatalf("fresh blob: %d", status)
	}
}
