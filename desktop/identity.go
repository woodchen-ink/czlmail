package main

import (
	"context"
	"errors"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/keychain"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// identitySettings 是随登录身份走的设置: 跨设备同步的 AI / MCP / 日记设置 (会写回该身份的网盘)、
// 静音与默认发件身份 (按账号 id 记)、推送订阅。界面偏好 (已读方式、提醒开关、头像开关等) 属于这台设备, 不清。
func identitySettings() []string {
	keys := append([]string{}, aiSyncedSettings...)
	return append(keys, settingAIKeyUpdatedAt, "mutedMailAccounts", settingUnifiedExcluded, "defaultIdentities", settingPushRelay)
}

// primaryOnlySettings 是 identitySettings 里只属于主登录、可以整个删的部分 (同步的 AI 设置);
// 按账号 id 记的列表类设置不在其中, 还有别的登录时只去掉主登录的条目 (forgetAccountSettings);
// 推送登记按登录分开存, 主登录那份由 forgetPush 删。
func primaryOnlySettings() []string {
	keys := append([]string{}, aiSyncedSettings...)
	return append(keys, settingAIKeyUpdatedAt)
}

// hasOtherSignIns 报告除主登录外是否还有登录 (#15)。
func (a *App) hasOtherSignIns() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.extra) > 0
}

// ensureCacheOwner 在会话建立后、同步开始前确认本机缓存属于这次登录的身份, 不是就清空。
//
// 退出登录保留缓存, 同一个人重新登录不必重新引导; 但换成另一个人 (或另一台服务器) 时,
// 上一个人的邮件会混进新账号的列表, 本机的模板与 AI 设置 (含 API Key) 还会被同步写进新账号的网盘。
// 旧版没有记录归属: 缓存里有本次会话看不到的账号时同样视为换人 (宁可多引导一次, 也不混用别人的缓存)。
func (a *App) ensureCacheOwner(ctx context.Context, st *store.Store, cfg config.Config, client *jmap.Client) error {
	owner := cfg.SessionEndpoint + "\x00" + strings.ToLower(cfg.Username)
	cur, err := st.StringSetting(ctx, store.SettingCacheOwner)
	if err != nil && !errors.Is(err, store.ErrNotFound) { // 没有记录 = 旧缓存或新装, 按空处理
		return err
	}
	if cur == owner {
		return nil
	}
	if cur == "" && !a.cacheFromOtherIdentity(ctx, st, client) {
		return st.SetStringSetting(ctx, store.SettingCacheOwner, owner)
	}

	a.log.Info("signed in as a different identity, clearing local cache")
	a.forgetPush(ctx, st)
	if a.hasOtherSignIns() {
		// 别的登录 (#15) 的缓存、受信名单、译文不属于主登录的上一个人, 留着; 只清主登录的那份。
		if err := st.ResetPrimaryIdentityData(ctx, primaryOnlySettings()); err != nil {
			return err
		}
		if err := a.forgetAccountSettings(st, func(acc string) bool {
			prefix, _ := accountid.Split(acc)
			return prefix == accountid.Primary
		}); err != nil {
			return err
		}
	} else if err := st.ResetIdentityData(ctx, identitySettings()); err != nil {
		return err
	}
	// 账号名与颜色、收藏的文件夹同步自主登录的网盘 (account-settings.json), 换了人改从新网盘取, 上一个人的不留。
	for _, prefix := range []string{accountLookPrefix, favoriteFolderPrefix} {
		if err := st.DeleteSettingsWithPrefix(ctx, prefix); err != nil {
			return err
		}
	}
	if err := keychain.Delete(config.KeyringService, aiKeyringKey); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		a.log.Warn("clear ai key", "err", err)
	}
	resetOwnAddresses()
	return st.SetStringSetting(ctx, store.SettingCacheOwner, owner)
}

// cacheFromOtherIdentity 用于没有归属记录的旧缓存: 缓存里有本次会话没有的账号。
// 已经混进新账号的旧缓存 (换人登录过一次) 也能认出来; 同一个人被收回某个共享邮箱时多引导一次, 无害。
func (a *App) cacheFromOtherIdentity(ctx context.Context, st *store.Store, client *jmap.Client) bool {
	cached, err := st.Accounts(ctx)
	if err != nil || client.Session == nil {
		return false
	}
	for _, acc := range cached {
		// 附加登录的账号 (带前缀) 本来就不在主登录的会话里。
		if prefix, _ := accountid.Split(acc.ID); prefix != accountid.Primary {
			continue
		}
		if _, ok := client.Session.Accounts[jmap.ID(acc.ID)]; !ok {
			return true
		}
	}
	return false
}
