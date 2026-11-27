package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 账号的名字与颜色 (设置 → 账户 → 名称与颜色): 用户给账号、给附加登录在侧栏里那一节起的名字与挑的颜色。
// 服务器上的账号名不变, 只影响本程序的显示。
//
// 跨设备同步: 存成个人网盘的 CZL Mail/account-settings.json (网盘里一类设置一个文件, 读写见 drive_sync.go),
// 本机每条一行 app_settings (accountLook:<同步键>), 逐条"最后修改者胜"; 清除写空值 (墓碑), 删除才能传到别的设备。
//
// 本机账号 id 的登录前缀 (s2 …) 是每台设备自己编的号, 不能作同步的键。同步键用会话地址 + 服务器上的账号 id,
// 登录那一节用会话地址 + "@" + 用户名; 同一个邮箱在每台设备上键都一样。界面仍按本机账号 id
// (登录则是 "signin:" + 前缀) 读写, 换算在这里做。会话地址不同的设备 (如网关在每台机器上地址不同) 对不上, 各管各的。

const (
	accountSettingsFileName = "account-settings.json"
	accountSettingsFileType = "czlmail-account-settings"

	// accountLookPrefix 是本机 app_settings 里账号名与颜色的键前缀, 后接同步键。
	accountLookPrefix = "accountLook:"
	// signInLookPrefix 是界面上登录那一节的键前缀, 后接登录前缀。
	signInLookPrefix = "signin:"
)

// accountColors 是可选的账号颜色, 与前端 lib/account-looks.ts、globals.css 的 --account-* 一致。
var accountColors = []string{"blue", "green", "amber", "violet", "red", "gray"}

// AccountLook 是一个账号 (或登录) 的显示名与颜色; 颜色是 accountColors 里的键名。
type AccountLook struct {
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
}

func (l AccountLook) empty() bool { return l.Name == "" && l.Color == "" }

// normalizeAccountLook 去掉首尾空白, 名字最多 64 个字符, 不认识的颜色丢掉。
func normalizeAccountLook(l AccountLook) AccountLook {
	l.Name = strings.TrimSpace(l.Name)
	if r := []rune(l.Name); len(r) > 64 {
		l.Name = string(r[:64])
	}
	if !slices.Contains(accountColors, l.Color) {
		l.Color = ""
	}
	return l
}

// encodeAccountLook 是一条设置的存储值: 空的写空串 (墓碑), 否则是 JSON。
func encodeAccountLook(l AccountLook) string {
	if l.empty() {
		return ""
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func decodeAccountLook(v string) AccountLook {
	var l AccountLook
	if v != "" {
		_ = json.Unmarshal([]byte(v), &l)
	}
	return normalizeAccountLook(l)
}

// lookEndpoint 是同步键里的会话地址: 大小写与结尾斜杠不同的写法视为同一个。
func lookEndpoint(cfg config.Config) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(cfg.SessionEndpoint)), "/")
}

// lookSignIn 是一个登录在同步键里的身份。
type lookSignIn struct {
	id, endpoint, user string
}

// syncKey 是这个登录下账号 (serverID) 或登录本身 (signIn) 的同步键。
func (s lookSignIn) syncKey(serverID string, signIn bool) string {
	if signIn {
		return s.endpoint + "#@" + strings.ToLower(s.user)
	}
	return s.endpoint + "#" + serverID
}

// lookSignIns 列出本机的登录 (已移除或还没有会话地址的除外)。
func (a *App) lookSignIns() []lookSignIn {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []lookSignIn
	for _, si := range a.signInsLocked() {
		if ep := lookEndpoint(si.cfg); !si.removed && ep != "" {
			out = append(out, lookSignIn{id: si.id, endpoint: ep, user: si.cfg.Username})
		}
	}
	return out
}

// localAccountLooks 把按同步键存的条目换算成界面用的本机键; 对不上本机任何登录的条目不出现。
// 同一台服务器登了两个人时, 共享邮箱在两个登录下各有一个本机 id, 用的是同一条。
func localAccountLooks(rows map[string]store.TimedSetting, signIns []lookSignIn) map[string]AccountLook {
	out := map[string]AccountLook{}
	for key, v := range rows {
		l := decodeAccountLook(v.Value)
		endpoint, rest, ok := strings.Cut(key, "#")
		if l.empty() || !ok {
			continue
		}
		user, isSignIn := strings.CutPrefix(rest, "@")
		for _, si := range signIns {
			switch {
			case si.endpoint != endpoint:
			case isSignIn:
				if strings.ToLower(si.user) == user {
					out[signInLookPrefix+si.id] = l
				}
			default:
				out[accountid.Join(si.id, rest)] = l
			}
		}
	}
	return out
}

// GetAccountLooks 返回账号与登录的名字、颜色, 键是本机账号 id 或 "signin:" + 登录前缀。
func (a *App) GetAccountLooks() (map[string]AccountLook, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	rows, err := st.TimedSettingsWithPrefix(a.ctx, accountLookPrefix)
	if err != nil {
		return nil, err
	}
	return localAccountLooks(rows, a.lookSignIns()), nil
}

// SetAccountLook 设置一个账号 (或登录那一节) 的名字与颜色, 都为空即清除。只改这一条, 不碰别的账号。
func (a *App) SetAccountLook(key string, look AccountLook) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	syncKey, err := a.accountLookSyncKey(key)
	if err != nil {
		return err
	}
	value := encodeAccountLook(normalizeAccountLook(look))
	cur, err := st.StringSetting(a.ctx, accountLookPrefix+syncKey)
	switch {
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	// 没变就不写: 修改时间是同步时比新旧的依据, 白白刷新会盖掉别的设备更晚的修改。
	case err == nil && cur == value, errors.Is(err, store.ErrNotFound) && value == "":
		return nil
	}
	if err := st.SetStringSetting(a.ctx, accountLookPrefix+syncKey, value); err != nil {
		return err
	}
	a.emit(EventSettingsChanged)
	a.syncAccountSettingsSoon()
	return nil
}

// accountLookSyncKey 把界面的本机键换成同步键。
func (a *App) accountLookSyncKey(key string) (string, error) {
	prefix, serverID := accountid.Split(key)
	id, isSignIn := strings.CutPrefix(key, signInLookPrefix)
	if isSignIn {
		prefix = id
	}
	for _, si := range a.lookSignIns() {
		if si.id == prefix {
			return si.syncKey(serverID, isSignIn), nil
		}
	}
	return "", fmt.Errorf("%w %q", errUnknownSignIn, key)
}

type accountSettingsFile struct {
	Version    int                            `json:"version"`
	Type       string                         `json:"type"`
	ExportedAt string                         `json:"exportedAt"`
	Looks      map[string]accountSettingsLook `json:"looks"`
}

// accountSettingsLook 是文件里的一条; 名字与颜色都空是墓碑。
type accountSettingsLook struct {
	Name      string `json:"name,omitempty"`
	Color     string `json:"color,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// syncAccountSettingsSoon 在后台同步账户设置, 失败只记日志。
func (a *App) syncAccountSettingsSoon() {
	go func() {
		if err := a.SyncAccountSettings(); errors.Is(err, errNoDriveSync) {
			a.log.Debug("sync account settings skipped", "err", err)
		} else if err != nil {
			a.log.Warn("sync account settings", "err", err)
		}
	}()
}

// SyncAccountSettings 立即与个人网盘里的 account-settings.json 合并。
func (a *App) SyncAccountSettings() error {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()

	st, err := a.currentStore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()

	file, data, err := a.openDriveSyncFile(ctx, accountSettingsFileName)
	if err != nil {
		return err
	}
	remote := map[string]store.TimedSetting{}
	if data != nil {
		var f accountSettingsFile
		if err := json.Unmarshal(data, &f); err != nil || f.Type != accountSettingsFileType {
			return fmt.Errorf("2165 %s/%s is not an account settings file", driveSyncFolder, accountSettingsFileName)
		}
		for k, e := range f.Looks {
			remote[k] = store.TimedSetting{
				Value:     encodeAccountLook(normalizeAccountLook(AccountLook{Name: e.Name, Color: e.Color})),
				UpdatedAt: parseISO(e.UpdatedAt),
			}
		}
	}

	local, err := st.TimedSettingsWithPrefix(ctx, accountLookPrefix)
	if err != nil {
		return err
	}
	merged, changes, remoteChanged := mergeTimed(local, remote, nil)

	if len(changes) > 0 {
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			for k, v := range changes {
				if err := store.PutTimedSetting(ctx, tx, accountLookPrefix+k, v); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		a.emit(EventSettingsChanged)
	}

	if !remoteChanged && file.exists() {
		return nil
	}
	out := accountSettingsFile{
		Version: 1, Type: accountSettingsFileType, ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Looks: make(map[string]accountSettingsLook, len(merged)),
	}
	for k, v := range merged {
		l := decodeAccountLook(v.Value)
		out.Looks[k] = accountSettingsLook{Name: l.Name, Color: l.Color, UpdatedAt: isoTime(v.UpdatedAt)}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return file.write(ctx, body)
}
