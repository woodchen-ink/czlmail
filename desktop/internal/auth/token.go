package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
)

// SaveFunc 在令牌被刷新后收到新令牌, 由调用方负责落到系统密钥库。
type SaveFunc func(*oauth2.Token) error

// ErrNoRefreshToken 访问令牌已不可用, 手里又没有刷新令牌: 只能重新登录。
var ErrNoRefreshToken = errors.New("3030 access token expired and no refresh token is stored")

// TokenSource 自动续期访问令牌, 并在令牌变化时落盘。
//
// 不直接用 x/oauth2 的 ReuseTokenSource, 两个原因:
//   - 它续期后不通知调用方, 而服务器可能启用刷新令牌轮换 —— 每次刷新都会作废旧的
//     refresh token。不把新值存下来, 应用一旦重启就只剩一个已失效的令牌。
//   - 它只按过期时间判断要不要续期。令牌在服务端被提前作废(管理员撤销、服务器迁移)时
//     本地看它仍在有效期内, 需要 MarkStale 强制换一次, 才能确认是不是真的要重新登录。
type TokenSource struct {
	ctx  context.Context
	cfg  *oauth2.Config
	save SaveFunc

	mu    sync.Mutex
	cur   *oauth2.Token
	stale bool
}

// MarkStale 让下一次取令牌时强制用刷新令牌换新的, 不管当前访问令牌是否到期。
// 服务器对一个本地看来没过期的令牌回 401 时调用。
func (s *TokenSource) MarkStale() {
	s.mu.Lock()
	s.stale = true
	s.mu.Unlock()
}

func (s *TokenSource) Token() (*oauth2.Token, error) {
	// 持锁刷新: 并发请求同时发现令牌过期时只换一次, 轮换刷新令牌的服务器不允许旧值用两遍。
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.stale && s.cur.Valid() {
		return s.cur, nil
	}
	if s.cur == nil || s.cur.RefreshToken == "" {
		return nil, ErrNoRefreshToken
	}

	tok, err := s.cfg.TokenSource(s.ctx, &oauth2.Token{RefreshToken: s.cur.RefreshToken}).Token()
	if err != nil {
		return nil, err
	}
	s.cur = tok
	s.stale = false
	if s.save != nil {
		// 落盘失败不应打断请求 —— 令牌本身是有效的, 最坏结果是下次启动要重新登录。
		// 由调用方的 SaveFunc 自行记录日志。
		_ = s.save(tok)
	}
	return tok, nil
}

// TokenSource 构造带落盘的自动续期令牌源。
func (c *Config) TokenSource(ctx context.Context, tok *oauth2.Token, save SaveFunc) *TokenSource {
	return &TokenSource{ctx: ctx, cfg: c.oauth2Config(), cur: tok, save: save}
}

// HTTPClient 返回一个会自动附带并续期访问令牌的 HTTP 客户端,
// 可直接赋给 jmap.Client.HttpClient。
func HTTPClient(ctx context.Context, src *TokenSource) *http.Client {
	return oauth2.NewClient(ctx, src)
}
