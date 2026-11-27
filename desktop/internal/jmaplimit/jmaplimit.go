// Package jmaplimit 让一个会话同时在途的 JMAP API 请求不超过服务器公布的 maxConcurrentRequests。
//
// 超出上限时服务器直接回 400 (urn:ietf:params:jmap:error:limit), 不排队 (RFC 8620 §3.6.1)。
// 重新读会话、连上后的全账号同步会同时发出好几个请求, 名额又按服务器上的用户计,
// 同一个人的另一台设备或另一个客户端也占着名额, 所以本机先排队, 服务器仍说超限时稍等重试几次。
// 只管 API 请求: 推送是长连接, 上传下载另有限额, 都不经过这里的名额。
package jmaplimit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultMax 是服务器没有公布上限时的并发数 (RFC 8620 建议至少 4)。
const DefaultMax = 4

// maxRetries 是服务器回超限后最多重试的次数。
const maxRetries = 3

// Session 返回当前会话的 API 地址与并发上限; 还没有会话时 apiURL 为空。
// 每个请求都会调用, 会话在运行中可能被替换 (重新读会话), 不能缓存。
type Session func() (apiURL string, max int)

// Wrap 返回一个带并发限制的客户端副本, 其余设置 (超时、跳转) 与 client 相同。
func Wrap(client *http.Client, session Session) *http.Client {
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped := *client
	wrapped.Transport = &transport{base: base, session: session, sleep: sleepCtx}
	return &wrapped
}

type transport struct {
	base    http.RoundTripper
	session Session
	slots   slots
	// sleep 在重试前等待, 测试里换成不等。
	sleep func(ctx context.Context, d time.Duration) error
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	apiURL, max := t.session()
	if req.Method != http.MethodPost || apiURL == "" || req.URL.String() != apiURL {
		return t.base.RoundTrip(req)
	}
	if max <= 0 {
		max = DefaultMax
	}
	ctx := req.Context()
	for attempt := 0; ; attempt++ {
		if err := t.slots.acquire(ctx, max); err != nil {
			return nil, err
		}
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			t.slots.release()
			return nil, err
		}
		if resp.StatusCode != http.StatusBadRequest {
			// 名额占到响应体读完: 服务器写完响应才算这个请求结束。
			resp.Body = &releaseOnClose{ReadCloser: resp.Body, release: t.slots.release}
			return resp, nil
		}
		// 问题详情很小, 读完再判断; 不是超限就原样交回调用方。
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		t.slots.release()
		if readErr == nil && attempt < maxRetries && req.GetBody != nil && concurrencyLimit(resp.Header, body) {
			if err := t.sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			next := req.Clone(ctx)
			if next.Body, err = req.GetBody(); err != nil {
				return nil, err
			}
			req = next
			continue
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
}

// concurrencyLimit 判断 400 是不是「同时进行的请求太多」。
func concurrencyLimit(h http.Header, body []byte) bool {
	if !strings.Contains(h.Get("Content-Type"), "json") {
		return false
	}
	var p struct {
		Type  string `json:"type"`
		Limit string `json:"limit"`
	}
	return json.Unmarshal(body, &p) == nil &&
		p.Type == "urn:ietf:params:jmap:error:limit" && p.Limit == "maxConcurrentRequests"
}

// backoff 是第 attempt 次重试前的等待: 0.5 秒起翻倍, 加一点随机, 免得几个请求同时再撞上。
func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << attempt
	return d + rand.N(d/2)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slots 是按先来后到分配的名额; 上限每次取用时传入, 会话换了上限也能跟着变。
type slots struct {
	mu      sync.Mutex
	active  int
	waiters []chan struct{}
}

func (s *slots) acquire(ctx context.Context, max int) error {
	s.mu.Lock()
	if s.active < max && len(s.waiters) == 0 {
		s.active++
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for i, w := range s.waiters {
			if w == ch {
				s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
				s.mu.Unlock()
				return ctx.Err()
			}
		}
		s.mu.Unlock()
		// 取消的同时已经被分到了名额: 交还给下一个。
		s.release()
		return ctx.Err()
	}
}

// release 交还一个名额; 有人在等时直接转给排在最前的, active 不变。
func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiters) > 0 {
		ch := s.waiters[0]
		s.waiters = s.waiters[1:]
		close(ch)
		return
	}
	if s.active > 0 {
		s.active--
	}
}

// releaseOnClose 在响应体关闭时交还名额, 只交还一次。
type releaseOnClose struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (r *releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}
