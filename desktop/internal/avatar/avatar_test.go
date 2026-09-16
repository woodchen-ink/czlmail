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
