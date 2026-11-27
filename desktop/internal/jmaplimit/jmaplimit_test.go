package jmaplimit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const limitBody = `{"type":"urn:ietf:params:jmap:error:limit","limit":"maxConcurrentRequests","status":400}`

func newClient(t *testing.T, apiURL string, max int) *http.Client {
	t.Helper()
	c := Wrap(&http.Client{}, func() (string, int) { return apiURL, max })
	c.Transport.(*transport).sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

func post(c *http.Client, url string) (*http.Response, error) {
	return c.Post(url, "application/json", bytes.NewReader([]byte(`{"using":[],"methodCalls":[]}`)))
}

// 同时在途的 API 请求不超过上限, 多出来的排队, 最后都完成。
func TestConcurrencyCap(t *testing.T) {
	var cur, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	api := srv.URL + "/jmap/api"
	c := newClient(t, api, 2)

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			resp, err := post(c, api)
			if err != nil {
				errs <- err
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if p := peak.Load(); p > 2 {
		t.Fatalf("peak concurrency %d, want at most 2", p)
	}
}

// 服务器回超限时重试, 其它 400 原样交回且不重试。
func TestRetryOnLimit(t *testing.T) {
	var calls atomic.Int32
	failFirst := int32(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("retried request has no body")
		}
		switch n := calls.Add(1); {
		case r.URL.Path == "/other":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"urn:ietf:params:jmap:error:notRequest","status":400}`)
		case n <= failFirst:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, limitBody)
		default:
			_, _ = io.WriteString(w, "{}")
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL+"/jmap/api", 4)
	resp, err := post(c, srv.URL+"/jmap/api")
	if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("limit retry: resp=%v err=%v calls=%d", resp, err, calls.Load())
	}
	resp.Body.Close()

	// 一直超限: 重试 maxRetries 次后把最后的 400 交回。
	calls.Store(0)
	failFirst = 100
	resp, err = post(c, srv.URL+"/jmap/api")
	if err != nil || resp.StatusCode != http.StatusBadRequest || calls.Load() != maxRetries+1 {
		t.Fatalf("persistent limit: resp=%v err=%v calls=%d", resp, err, calls.Load())
	}
	if body, _ := io.ReadAll(resp.Body); string(body) != limitBody {
		t.Fatalf("persistent limit body = %q", body)
	}

	// 不是 API 地址的请求不经过这里, 也不重试。
	c = newClient(t, srv.URL+"/other", 4)
	calls.Store(0)
	resp, err = post(c, srv.URL+"/other")
	if err != nil || resp.StatusCode != http.StatusBadRequest || calls.Load() != 1 {
		t.Fatalf("other 400: resp=%v err=%v calls=%d", resp, err, calls.Load())
	}
	if body, _ := io.ReadAll(resp.Body); !bytes.Contains(body, []byte("notRequest")) {
		t.Fatalf("other 400 body = %q", body)
	}
}

// 上传等非 API 请求不占名额; 排队时取消请求立即返回, 名额不泄漏。
func TestNonAPIAndCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jmap/api" {
			<-release
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()
	api := srv.URL + "/jmap/api"
	c := newClient(t, api, 1)

	first := make(chan error, 1)
	go func() {
		resp, err := post(c, api)
		if err == nil {
			resp.Body.Close()
		}
		first <- err
	}()
	// 等第一个请求占上名额。
	tr := c.Transport.(*transport)
	for deadline := time.Now().Add(2 * time.Second); ; {
		tr.slots.mu.Lock()
		busy := tr.slots.active == 1
		tr.slots.mu.Unlock()
		if busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first request never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if resp, err := post(c, srv.URL+"/jmap/upload/a/"); err != nil {
		t.Fatalf("upload blocked by the API limit: %v", err)
	} else {
		resp.Body.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, api, bytes.NewReader([]byte("{}")))
	if _, err := c.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued request after cancel: %v", err)
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	tr.slots.mu.Lock()
	active, waiting := tr.slots.active, len(tr.slots.waiters)
	tr.slots.mu.Unlock()
	if active != 0 || waiting != 0 {
		t.Fatalf("slots leaked: active=%d waiting=%d", active, waiting)
	}
}
