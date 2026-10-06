package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// AI 设置与 MCP 设置跨设备同步, 做法与邮件模板一样: 存成个人网盘里的
// CZL Mail/ai-settings.json, 逐项"最后修改者胜", 时间取 app_settings.updated_at。
//
// API Key 也一并同步, 否则换台设备还得重新粘贴一遍; 本地仍只存系统钥匙串,
// 它的修改时间另记在 aiApiKeyUpdatedAt 里。清除 Key 以空值同步, 别的设备也会清掉。
// MCP 令牌不同步: 它只对本机的端点有效, 各设备的客户端配置各自独立。

const (
	aiSettingsFileName = "ai-settings.json"
	aiSettingsFileType = "czlmail-ai-settings"

	// aiSyncAPIKey 是 API Key 在同步文件里的键名, 不对应 app_settings 的行。
	aiSyncAPIKey = "aiApiKey"
	// settingAIKeyUpdatedAt 记录本地 API Key 的修改时间(行的 updated_at)。
	settingAIKeyUpdatedAt = "aiApiKeyUpdatedAt"

	// EventAISettingsChanged AI 或 MCP 设置被同步改动, 界面应重新读取。
	EventAISettingsChanged = "ai-settings:changed"
)

// aiSyncedSettings 是参与同步的 app_settings 键。
var aiSyncedSettings = []string{
	"aiEnabled", "aiBaseUrl", "aiModel", "aiTranslateLang", "aiReasoningTranslate", "aiReasoningWrite",
	store.SettingMCPEnabled, store.SettingMCPAllowSend, store.SettingMCPAllowModify,
}

var aiMCPSettings = []string{store.SettingMCPEnabled, store.SettingMCPAllowSend, store.SettingMCPAllowModify}

type aiSettingsFile struct {
	Version    int                       `json:"version"`
	Type       string                    `json:"type"`
	ExportedAt string                    `json:"exportedAt"`
	Settings   map[string]aiSettingEntry `json:"settings"`
}

type aiSettingEntry struct {
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
}

// syncAISettingsSoon 在后台同步 AI 与 MCP 设置, 失败只记日志。
func (a *App) syncAISettingsSoon() {
	go func() {
		if err := a.SyncAISettings(); err != nil {
			a.log.Warn("sync ai settings", "err", err)
		}
	}()
}

// SyncAISettings 立即与服务端合并 AI 与 MCP 设置。
func (a *App) SyncAISettings() error {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()

	st, err := a.currentStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()

	file, data, err := a.openDriveSyncFile(ctx, aiSettingsFileName)
	if err != nil {
		return err
	}
	remote := map[string]store.TimedSetting{}
	if data != nil {
		var f aiSettingsFile
		if err := json.Unmarshal(data, &f); err != nil || f.Type != aiSettingsFileType {
			return fmt.Errorf("2163 %s/%s is not an AI settings file", driveSyncFolder, aiSettingsFileName)
		}
		for k, e := range f.Settings {
			remote[k] = store.TimedSetting{Value: e.Value, UpdatedAt: parseISO(e.UpdatedAt)}
		}
	}

	local, err := a.localAISettings(ctx, st)
	if err != nil {
		return err
	}
	merged, changes, remoteChanged := mergeSettings(local, remote)

	if len(changes) > 0 {
		if err := a.applyAISettings(ctx, st, changes); err != nil {
			return err
		}
	}

	if !remoteChanged && file.exists() {
		return nil
	}
	out := aiSettingsFile{
		Version: 1, Type: aiSettingsFileType, ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Settings: make(map[string]aiSettingEntry, len(merged)),
	}
	for k, v := range merged {
		out.Settings[k] = aiSettingEntry{Value: v.Value, UpdatedAt: isoTime(v.UpdatedAt)}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return file.write(ctx, body)
}

// localAISettings 读取本地参与同步的设置, 含钥匙串里的 API Key。
func (a *App) localAISettings(ctx context.Context, st *store.Store) (map[string]store.TimedSetting, error) {
	local, err := st.TimedSettings(ctx, append(slices.Clone(aiSyncedSettings), settingAIKeyUpdatedAt))
	if err != nil {
		return nil, err
	}
	stamp, hasStamp := local[settingAIKeyUpdatedAt]
	delete(local, settingAIKeyUpdatedAt)
	key, err := keyring.Get(config.KeyringService, aiKeyringKey)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("2201 read api key: %w", err)
	}
	// 清除过的 Key 也要以空值参与合并, 删除才能传到别的设备。
	if key != "" || hasStamp {
		local[aiSyncAPIKey] = store.TimedSetting{Value: key, UpdatedAt: stamp.UpdatedAt}
	}
	return local, nil
}

// applyAISettings 把远端较新的设置写回本地, 并让 MCP 端点跟上新的开关。
func (a *App) applyAISettings(ctx context.Context, st *store.Store, changes map[string]store.TimedSetting) error {
	if v, ok := changes[aiSyncAPIKey]; ok {
		if v.Value == "" {
			if err := keyring.Delete(config.KeyringService, aiKeyringKey); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				return fmt.Errorf("2201 delete api key: %w", err)
			}
		} else if err := keyring.Set(config.KeyringService, aiKeyringKey, v.Value); err != nil {
			return fmt.Errorf("2201 save api key to system keychain: %w", err)
		}
	}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		for k, v := range changes {
			if k == aiSyncAPIKey {
				k, v = settingAIKeyUpdatedAt, store.TimedSetting{Value: strconv.FormatInt(v.UpdatedAt, 10), UpdatedAt: v.UpdatedAt}
			}
			if err := store.PutTimedSetting(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	for _, k := range aiMCPSettings {
		if _, ok := changes[k]; ok {
			a.restartMCPFromSettings(st)
			break
		}
	}
	a.emit(EventAISettingsChanged)
	return nil
}

// restartMCPFromSettings 按当前设置启停 MCP 端点。权限在每次调用工具时读取, 不必重启。
func (a *App) restartMCPFromSettings(st *store.Store) {
	if on, _ := st.BoolSetting(a.ctx, store.SettingMCPEnabled, false); on {
		if err := a.startMCP(); err != nil {
			a.log.Error("start mcp", "err", err)
		}
	} else {
		a.stopMCP()
	}
}

// markAIKeyChanged 记下本地 API Key 的修改时间, 同步时据此判断哪边较新。
func markAIKeyChanged(ctx context.Context, st *store.Store) error {
	return st.SetStringSetting(ctx, settingAIKeyUpdatedAt, strconv.FormatInt(time.Now().Unix(), 10))
}

// mergeSettings 逐项合并本地与远端, 返回合并结果、需要写回本地的项, 以及远端是否需要更新。
// 只在一边存在的项取那一边; 两边都有时取修改时间较新的, 相同时不动。
// 远端有而本程序不认识的键原样保留, 新版本加的设置不会被旧版本抹掉。
func mergeSettings(local, remote map[string]store.TimedSetting) (map[string]store.TimedSetting, map[string]store.TimedSetting, bool) {
	known := map[string]bool{aiSyncAPIKey: true}
	for _, k := range aiSyncedSettings {
		known[k] = true
	}
	merged := make(map[string]store.TimedSetting, len(remote)+len(local))
	changes := map[string]store.TimedSetting{}
	remoteChanged := false

	for k, r := range remote {
		merged[k] = r
		l, ok := local[k]
		switch {
		case !known[k]:
		case !ok || r.UpdatedAt > l.UpdatedAt:
			if !ok || r.Value != l.Value {
				changes[k] = r
			}
		case l.UpdatedAt > r.UpdatedAt:
			merged[k] = l
			if l.Value != r.Value {
				remoteChanged = true
			}
		}
	}
	for k, l := range local {
		if _, ok := remote[k]; !ok {
			merged[k] = l
			remoteChanged = true
		}
	}
	return merged, changes, remoteChanged
}
