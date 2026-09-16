package auth

import (
	"context"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
)

// SaveFunc 在令牌被刷新后收到新令牌, 由调用方负责落到系统密钥库。
type SaveFunc func(*oauth2.Token) error

// persistingSource 包住 oauth2 的自动续期 TokenSource, 在令牌变化时落盘。
//
// 之所以必须包一层: x/oauth2 在访问令牌过期时会静默用 refresh token 换新的,
// 但不会通知调用方。而服务器可能启用了刷新令牌轮换 —— 每次刷新都会作废旧的
// refresh token。不把新值存下来, 应用一旦重启就只剩一个已失效的令牌, 表现为
// 用了一阵子之后突然要求重新登录。
type persistingSource struct {
	inner oauth2.TokenSource
	save  SaveFunc

	mu   sync.Mutex
	last *oauth2.Token
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := s.inner.Token()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.last == nil || tok.AccessToken != s.last.AccessToken || tok.RefreshToken != s.last.RefreshToken {
		s.last = tok
		if s.save != nil {
			// 落盘失败不应打断请求 —— 令牌本身是有效的, 最坏结果是下次启动要重新登录。
			// 由调用方的 SaveFunc 自行记录日志。
			_ = s.save(tok)
		}
	}
	return tok, nil
}

// HTTPClient 返回一个会自动附带并续期访问令牌的 HTTP 客户端,
// 可直接赋给 jmap.Client.HttpClient。
func (c *Config) HTTPClient(ctx context.Context, tok *oauth2.Token, save SaveFunc) *http.Client {
	src := &persistingSource{
		inner: c.oauth2Config().TokenSource(ctx, tok),
		last:  tok,
		save:  save,
	}
	return oauth2.NewClient(ctx, src)
}
