//go:build ios

package main

// 新邮件推送 (iOS)。程序退到后台几秒后就被系统挂起, EventSource 断开, 收不到新邮件。
// 改由邮件服务器把新邮件推到 CZL Connect 的推送中转 (文档 connect.czl.net/docs/push), 中转转成 APNs 通知,
// 原生壳的通知扩展解密后显示发件人与主题。
//
// 分工: 原生壳管设备令牌、推送密钥 (P-256 + auth, 存在与通知扩展共享的钥匙串) 与解密;
// Go 管中转登记与 JMAP 推送订阅 (draft-ietf-jmap-emailpush, 逻辑在 push_relay.go, 每个登录一份), 内容按 RFC 8291 加密, 中转看不到。
//
// 订阅由 Stalwart 默认 7 天过期, 每次回到前台 (CzlResume) 与每 12 小时续期一次。
// 推送生效后不再由同步弹新邮件通知, 否则前台时同一封信会弹两次。

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

var pushEnabled = true

func init() {
	pushDeps.deviceInfo = func(ctx context.Context) (pushInfo, error) {
		raw, err := hostRequest(ctx, "pushInfo", struct{}{})
		if err != nil {
			return pushInfo{}, err
		}
		var info pushInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			return pushInfo{}, errors.New("bad device info")
		}
		return info, nil
	}
	pushDeps.decrypt = func(ctx context.Context, body string) (string, error) {
		plain, err := hostRequest(ctx, "pushDecrypt", map[string]string{"body": body})
		if err != nil {
			return "", err
		}
		var text string
		if err := json.Unmarshal(plain, &text); err != nil {
			return "", err
		}
		return text, nil
	}
}

// runPush 跟随一个登录的会话运行推送循环 (见 pushLoop)。
func (a *App) runPush(ctx context.Context, signIn string, s *syncer.Syncer, st *store.Store) {
	a.pushLoop(ctx, signIn, s, st)
}
