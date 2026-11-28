package avatar

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestCandidates(t *testing.T) {
	cases := []struct {
		email      string
		wantDomain string // 空串表示不应查域名图标
	}{
		{"noreply@newsletter.cloudflare.com", "cloudflare.com"}, // 取可注册域
		{"a@shop.example.co.uk", "example.co.uk"},               // 多段公共后缀
		{"someone@gmail.com", ""},                               // 免费邮箱不查图标
		{"someone@qq.com", ""},
	}
	for _, c := range cases {
		keys := candidates(c.email)
		if !strings.HasPrefix(keys[0], "g:") {
			t.Errorf("%s: Gravatar 应排第一, 实际 %v", c.email, keys)
		}
		got := ""
		if len(keys) > 1 {
			got = strings.TrimPrefix(keys[1], "f:")
			if len(keys) != 3 || keys[2] != "s:"+got {
				t.Errorf("%s: 直连图标 key 缺失, 实际 %v", c.email, keys)
			}
		}
		if got != c.wantDomain {
			t.Errorf("%s: 域名图标 key = %q, want %q", c.email, got, c.wantDomain)
		}
	}
}

// 打真实上游: 有 Gravatar 的地址、只有域名图标的地址、两者都没有的地址。
func TestIntegrationServe(t *testing.T) {
	if os.Getenv("CZLMAIL_TEST_HOST") == "" {
		t.Skip("set CZLMAIL_TEST_HOST to run")
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := New(func() *store.Store { return st }, func() bool { return true }, slog.Default())

	for _, c := range []struct {
		email string
		want  int
	}{
		{"wood@czl.net", http.StatusOK},
		{"noreply@notify.cloudflare.com", http.StatusOK},
		{"zz-nobody-91823@gmail.com", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path+"?email="+c.email, nil))
		t.Logf("%-32s -> %d %s %d bytes", c.email, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.email, rec.Code, c.want)
		}
	}

	// 第二次应命中缓存(包括负缓存), 不再访问上游。
	svc.client.Transport = failingTransport{}
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path+"?email=noreply@notify.cloudflare.com", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("缓存未命中, 重新访问了上游: %d", rec.Code)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrHandlerTimeout
}

func newTestService(t *testing.T, fetch func(key string) (*store.CachedAvatar, bool)) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := New(func() *store.Store { return st }, func() bool { return true }, slog.Default())
	svc.fetchKey = func(_ context.Context, key string) (*store.CachedAvatar, bool) { return fetch(key) }
	return svc, st
}

var png = &store.CachedAvatar{ContentType: "image/png", Data: []byte(strings.Repeat("x", 200))}

// 缓存过期时先返回旧图, 后台再拉; 冷却期内不重复拉。
func TestStaleAvatarServedWhileRefreshing(t *testing.T) {
	ctx := context.Background()
	fetched := make(chan string, 4)
	release := make(chan struct{})
	svc, st := newTestService(t, func(key string) (*store.CachedAvatar, bool) {
		fetched <- key
		<-release
		return png, true
	})
	if err := st.PutAvatar(ctx, "g:old", *png); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-positiveTTL - time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE avatar_cache SET fetched_at = ? WHERE key = 'g:old'`, old); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if img := svc.lookup(ctx, "g:old"); img == nil || img.Missing {
		t.Fatalf("stale image not served: %+v", img)
	}
	if time.Since(start) > time.Second {
		t.Fatal("lookup waited for the upstream")
	}
	select {
	case <-fetched:
	case <-time.After(2 * time.Second):
		t.Fatal("no background refresh")
	}
	close(release)

	// 冷却期内再看到过期缓存不再拉。
	svc.mu.Lock()
	svc.refreshed["g:other"] = time.Now()
	svc.mu.Unlock()
	svc.refresh(st, "g:other")
	select {
	case k := <-fetched:
		t.Fatalf("refreshed %s during cooldown", k)
	default:
	}
}

// Gravatar 命中时不等域名图标。
func TestFirstHitDoesNotWaitForLowerPriority(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	svc, _ := newTestService(t, func(key string) (*store.CachedAvatar, bool) {
		if strings.HasPrefix(key, "f:") {
			<-block
			return &store.CachedAvatar{Missing: true}, true
		}
		return png, true
	})
	done := make(chan *store.CachedAvatar, 1)
	go func() { done <- svc.firstHit(context.Background(), []string{"g:a", "f:b"}) }()
	select {
	case img := <-done:
		if img == nil {
			t.Fatal("no hit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waited for the lower-priority candidate")
	}
}
