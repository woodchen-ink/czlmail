package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/keychain"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncseal"
)

// 附加登录的跨设备同步: 在一台设备上添加的附加登录, 别的设备 (主登录是同一个人) 自动加上, 不用逐个再填。
//
// 存成主登录个人网盘的 CZL Mail/sign-ins.json (读写见 drive_sync.go)。服务器地址、用户名、登录方式明文存,
// 应用专用密码用同步口令加密 (internal/syncseal): 口令每台设备输一次, 推导出的密钥存本机密钥库, 口令本身不存。
// 浏览器授权的登录只同步配置, 令牌不同步 (动态注册的客户端与轮换的刷新令牌都属于一台设备), 加进来是「已退出」, 点登录授权一次。
//
// 逐条"最后修改者胜": 本机每条一行 app_settings (syncedSignIn:<同步键>, 值是配置 JSON, 移除写空串留墓碑),
// 添加、重新登录 (密码可能换了) 与移除时刷新; 同步键同 account-settings.json 的登录键 (会话地址 + "#@" + 用户名)。
// 别的设备移除的登录在本机也移除; 退出登录只影响本机。

const (
	signInSyncFileName = "sign-ins.json"
	signInSyncFileType = "czlmail-sign-ins"
	// syncedSignInPrefix 是本机 app_settings 里同步记录的键前缀, 后接同步键。
	syncedSignInPrefix = "syncedSignIn:"
	// signInSyncKeyEntry 是同步密钥在系统密钥库里的条目名。
	signInSyncKeyEntry = "sign-in-sync-key"
)

var (
	// errSignInSyncStale 本机的密钥与网盘里的对不上: 口令在别的设备上改过 (或重设了)。
	errSignInSyncStale = errors.New("2189 the sync passphrase was changed on another device; enter the new one")
	// errDriveNotReady 主登录的网盘还没同步完 (刚登录): 这时看不到网盘里已有的同步文件, 不能新建口令。
	errDriveNotReady = errors.New("2175 the personal files have not finished syncing yet; try again in a moment")
)

// syncedSignIn 是一个附加登录里可以同步的配置: 不含凭据, 也不含本机状态 (是否退出)。
type syncedSignIn struct {
	Server          string   `json:"server"`
	AuthMethod      string   `json:"authMethod"`
	SessionEndpoint string   `json:"sessionEndpoint"`
	Username        string   `json:"username"`
	Issuer          string   `json:"issuer,omitempty"`
	ClientID        string   `json:"clientId,omitempty"`
	RedirectURI     string   `json:"redirectUri,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

func syncedSignInOf(cfg config.Config) syncedSignIn {
	return syncedSignIn{
		Server: cfg.ServerHost, AuthMethod: cfg.AuthMethod, SessionEndpoint: cfg.SessionEndpoint, Username: cfg.Username,
		Issuer: cfg.Issuer, ClientID: cfg.ClientID, RedirectURI: cfg.RedirectURI, Scopes: cfg.Scopes,
	}
}

func (s syncedSignIn) config() config.Config {
	return config.Config{
		ServerHost: s.Server, AuthMethod: s.AuthMethod, SessionEndpoint: s.SessionEndpoint, Username: s.Username,
		Issuer: s.Issuer, ClientID: s.ClientID, RedirectURI: s.RedirectURI, Scopes: s.Scopes,
	}
}

func (s syncedSignIn) encode() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// decodeSyncedSignIn 解开一条记录; 墓碑 (空串) 与不完整的配置报告 false。
func decodeSyncedSignIn(v string) (syncedSignIn, bool) {
	var s syncedSignIn
	if v == "" || json.Unmarshal([]byte(v), &s) != nil {
		return s, false
	}
	ok := s.SessionEndpoint != "" && s.Username != "" &&
		(s.AuthMethod == config.AuthPassword || s.AuthMethod == config.AuthOAuth)
	return s, ok
}

// signInSyncKey 是一个登录的同步键, 与 account-settings.json 里登录那一节的键相同。
func signInSyncKey(cfg config.Config) string {
	return lookSignIn{endpoint: lookEndpoint(cfg), user: cfg.Username}.syncKey("", true)
}

type signInSyncFile struct {
	Version    int                        `json:"version"`
	Type       string                     `json:"type"`
	ExportedAt string                     `json:"exportedAt"`
	Encryption *syncseal.Params           `json:"encryption,omitempty"`
	SignIns    map[string]signInSyncEntry `json:"signIns"`
}

// signInSyncEntry 是文件里的一个登录; SignIn 为空是墓碑。
type signInSyncEntry struct {
	SignIn *syncedSignIn `json:"signIn,omitempty"`
	// Password 是用同步口令加密的应用专用密码 (附加数据为同步键); 浏览器授权的登录、或没有一台设备存着密码时为空。
	Password  string `json:"password,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

func parseSignInSyncFile(data []byte) (signInSyncFile, error) {
	f := signInSyncFile{SignIns: map[string]signInSyncEntry{}}
	if data == nil {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Type != signInSyncFileType {
		return f, fmt.Errorf("2168 %s/%s is not a sign-in sync file", driveSyncFolder, signInSyncFileName)
	}
	if f.SignIns == nil {
		f.SignIns = map[string]signInSyncEntry{}
	}
	return f, nil
}

// signInSyncKeyRecord 是本机密钥库里的同步密钥。
type signInSyncKeyRecord struct {
	// Owner 是主登录的同步键: 网盘属于主登录, 主登录换了人, 旧密钥就作废。
	Owner  string          `json:"owner"`
	Params syncseal.Params `json:"params"`
	Key    string          `json:"key"`
	key    []byte
}

// loadSignInSyncKey 读本机的同步密钥; 没开启 (或属于以前的主登录) 时返回 nil。
func (a *App) loadSignInSyncKey() (*signInSyncKeyRecord, error) {
	raw, err := keychain.Get(config.KeyringService, signInSyncKeyEntry)
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("2015 read credential from system keyring: %w", err)
	}
	var rec signInSyncKeyRecord
	if json.Unmarshal([]byte(raw), &rec) != nil || rec.Owner != signInSyncKey(a.primaryConfig()) {
		return nil, nil
	}
	if rec.key, err = base64.StdEncoding.DecodeString(rec.Key); err != nil || !syncseal.Verify(rec.key, rec.Params) {
		return nil, nil
	}
	return &rec, nil
}

func (a *App) saveSignInSyncKey(p syncseal.Params, key []byte) error {
	rec := signInSyncKeyRecord{Owner: signInSyncKey(a.primaryConfig()), Params: p, Key: base64.StdEncoding.EncodeToString(key)}
	raw, _ := json.Marshal(rec)
	if err := keychain.Set(config.KeyringService, signInSyncKeyEntry, string(raw)); err != nil {
		return fmt.Errorf("2018 write credential to system keyring: %w", err)
	}
	return nil
}

// driveReady 报告主登录个人网盘的文件列表是否已同步过一次。刚登录时本地还没有网盘的文件列表,
// 读不到网盘里已有的同步文件, 这时既不能判断「没开启」, 也不能写文件 (会建出第二个同名文件)。
func (a *App) driveReady(ctx context.Context) bool {
	st, err := a.currentStore()
	if err != nil {
		return false
	}
	accountID := a.personalAccountID(st)
	if accountID == "" {
		return false
	}
	state, err := st.State(ctx, accountID, store.TypeFileNode)
	return err == nil && state != ""
}

// SignInSyncStatus 是设置 → 账户里附加登录同步的状态。
type SignInSyncStatus struct {
	// State: unavailable 主登录没有网盘 (或未登录); loading 网盘还没同步完; damaged 网盘里的文件坏了, 只能重设;
	// off 没开启; locked 别的设备开启了, 本机还没输入口令; stale 口令在别的设备上改过; on 已开启。
	State string `json:"state"`
	// Pending 是网盘里有、本机还没有的附加登录 (用户名 · 服务器), 提示输入口令时列出。
	Pending []string `json:"pending"`
}

// GetSignInSync 读网盘里的同步文件, 报告附加登录同步的状态。
func (a *App) GetSignInSync() (SignInSyncStatus, error) {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	out := SignInSyncStatus{State: "unavailable", Pending: []string{}}
	// 网盘还没引导过 (刚登录): 不在这里抢着全量拉取 (大网盘 30 秒内拉不完, 白拉), 等主同步循环引导完。
	if !a.driveReady(ctx) {
		if st, err := a.currentStore(); err == nil {
			if acc := a.personalAccountID(st); acc != "" {
				if s, err := a.syncerFor(acc); err == nil && s.SupportsFiles(acc) {
					out.State = "loading"
				}
			}
		}
		return out, nil
	}
	_, data, err := a.openDriveSyncFile(ctx, signInSyncFileName)
	if errors.Is(err, errNoDriveSync) || errors.Is(err, errNotSignedIn) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	remote, err := parseSignInSyncFile(data)
	if err != nil {
		a.log.Warn("sign-in sync file unreadable", "err", err)
		out.State = "damaged"
		return out, nil
	}
	rec, err := a.loadSignInSyncKey()
	if err != nil {
		return out, err
	}
	switch {
	case rec == nil && remote.Encryption != nil:
		out.State = "locked"
	case rec == nil:
		out.State = "off"
	case remote.Encryption != nil && !remote.Encryption.SameKey(rec.Params):
		out.State = "stale"
	default:
		out.State = "on"
	}
	if f, err := config.LoadFile(); err == nil {
		for k, e := range remote.SignIns {
			if _, found := findSignInBySyncKey(f, k); e.SignIn != nil && !found {
				out.Pending = append(out.Pending, e.SignIn.Username+" · "+e.SignIn.Server)
			}
		}
	}
	slices.Sort(out.Pending)
	return out, nil
}

// EnableSignInSync 在本机开启附加登录同步: 网盘里已有同步 (别的设备开启的) 时核对口令, 没有时用这个口令新建。
// 开启后立即同步一次, 网盘里有、本机没有的附加登录随之加上。
func (a *App) EnableSignInSync(passphrase string) error {
	if err := a.unlockSignInSync(passphrase); err != nil {
		return err
	}
	return a.SyncSignIns()
}

func (a *App) unlockSignInSync(passphrase string) error {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	_, data, err := a.openDriveSyncFile(ctx, signInSyncFileName)
	if err != nil {
		return err
	}
	remote, err := parseSignInSyncFile(data)
	if err != nil {
		return err
	}
	var p syncseal.Params
	var key []byte
	if remote.Encryption == nil && !a.driveReady(ctx) {
		return errDriveNotReady
	}
	if remote.Encryption != nil {
		p = *remote.Encryption
		key, err = syncseal.Unlock(passphrase, p)
	} else {
		// 网盘里还没有: 新建参数, 文件在随后的同步里写出。
		p, key, err = syncseal.New(passphrase)
	}
	if err != nil {
		return err
	}
	a.log.Info("sign-in sync enabled", "new", remote.Encryption == nil)
	return a.saveSignInSyncKey(p, key)
}

// ResetSignInSync 换一个同步口令 (改口令或忘了旧口令)。本机的密钥还对得上网盘时, 已同步的密码全部改用新口令加密;
// 对不上时 (忘了口令) 只有本机存着的密码能重新加密, 只存在别的设备上的密码要在那台设备上输入新口令后补上。
// 别的设备之后要输入新口令。
func (a *App) ResetSignInSync(passphrase string) error {
	// 新参数与旧密钥在重试之外各取一次: 重试时本机已存成新密钥, 再读就分不出旧的了, 已同步的密码会被当成解不开丢掉。
	p, key, err := syncseal.New(passphrase)
	if err != nil {
		return err
	}
	old, err := a.loadSignInSyncKey()
	if err != nil {
		return err
	}
	if err := retryDriveSync(func() error { return a.resetSignInSync(p, key, old) }); err != nil {
		return err
	}
	return a.SyncSignIns()
}

func (a *App) resetSignInSync(p syncseal.Params, key []byte, old *signInSyncKeyRecord) error {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	file, data, err := a.openDriveSyncFile(ctx, signInSyncFileName)
	if err != nil {
		return err
	}
	if !a.driveReady(ctx) {
		return errDriveNotReady
	}
	remote, err := parseSignInSyncFile(data)
	if err != nil {
		// 文件坏了: 重设就是从空文件重新开始, 各设备之后把自己的附加登录再写进来。
		a.log.Warn("resetting unreadable sign-in sync file", "err", err)
		remote = signInSyncFile{SignIns: map[string]signInSyncEntry{}}
	}
	canOpen := old != nil && remote.Encryption != nil && remote.Encryption.SameKey(old.Params)
	for k, e := range remote.SignIns {
		if e.Password == "" {
			continue
		}
		e.Password = ""
		if canOpen {
			if pw, err := syncseal.Open(old.key, k, remote.SignIns[k].Password); err == nil {
				e.Password, _ = syncseal.Seal(key, k, pw)
			}
		}
		remote.SignIns[k] = e
	}
	remote.Version, remote.Type, remote.Encryption = 1, signInSyncFileType, &p
	remote.ExportedAt = time.Now().UTC().Format(time.RFC3339)
	body, err := json.MarshalIndent(remote, "", "  ")
	if err != nil {
		return err
	}
	// 先写网盘再存本机密钥: 写网盘失败 (超时、一直撞上别的设备的写入) 时什么都没变, 旧口令照常能用;
	// 存密钥失败时网盘已是新口令, 本机显示「口令已更改」, 输入刚设的口令就能接上。
	// 反过来的话写网盘失败后本机拿着网盘里没有的新密钥, 刚设的口令输进去也对不上。
	if err := file.write(ctx, body); err != nil {
		return err
	}
	a.log.Info("sign-in sync passphrase reset", "kept", canOpen)
	return a.saveSignInSyncKey(p, key)
}

// DisableSignInSync 在本机关闭附加登录同步 (删掉本机的密钥)。网盘里的数据与别的设备不受影响。
func (a *App) DisableSignInSync() error {
	err := keychain.Delete(config.KeyringService, signInSyncKeyEntry)
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return fmt.Errorf("2019 delete credential from system keyring: %w", err)
	}
	return nil
}

// syncSignInsSoon 在后台同步附加登录, 失败只记日志。
func (a *App) syncSignInsSoon() {
	go func() {
		if err := a.SyncSignIns(); errors.Is(err, errNoDriveSync) || errors.Is(err, errSignInSyncStale) {
			a.log.Debug("sync sign-ins skipped", "err", err)
		} else if err != nil {
			a.log.Warn("sync sign-ins", "err", err)
		}
	}()
}

// SyncSignIns 立即与网盘里的 sign-ins.json 合并, 并在本机加上、移除或更新相应的附加登录。本机没开启时什么都不做。
//
// 远端较新的条目先在本机做完 (加上、移除、更新密码) 再记进本机的同步记录; 没做成的不记, 下一轮还会再做,
// 不会因为写网盘超时或程序退出而永远漏掉一个登录。
func (a *App) SyncSignIns() error {
	var plan *signInSyncPlan
	err := retryDriveSync(func() (err error) {
		plan, err = a.mergeSignIns()
		return err
	})
	if err != nil || plan == nil {
		return err
	}
	done := map[string]store.TimedSetting{}
	for _, act := range plan.actions {
		if err := a.applySignInSync(act); err != nil {
			a.log.Warn("apply synced sign-in", "key", act.key, "err", err)
			continue
		}
		done[act.key] = plan.changes[act.key]
	}
	if len(done) == 0 {
		return nil
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	return st.WithTx(a.ctx, func(tx *sql.Tx) error {
		for k, v := range done {
			// 合并之后本机可能刚有更新的操作 (添加、移除), 不能拿较旧的远端记录盖掉它。
			var cur int64
			err := tx.QueryRowContext(a.ctx, `SELECT updated_at FROM app_settings WHERE key = ?`, syncedSignInPrefix+k).Scan(&cur)
			if err == nil && cur > v.UpdatedAt {
				continue
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := store.PutTimedSetting(a.ctx, tx, syncedSignInPrefix+k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *App) mergeSignIns() (*signInSyncPlan, error) {
	driveSyncMu.Lock()
	defer driveSyncMu.Unlock()

	rec, err := a.loadSignInSyncKey()
	if err != nil || rec == nil {
		return nil, err
	}
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()

	file, data, err := a.openDriveSyncFile(ctx, signInSyncFileName)
	if err != nil {
		return nil, err
	}
	remote, err := parseSignInSyncFile(data)
	if err != nil {
		return nil, err
	}
	if remote.Encryption != nil && !remote.Encryption.SameKey(rec.Params) {
		return nil, errSignInSyncStale
	}
	if !a.driveReady(ctx) {
		return nil, nil
	}

	cfgFile, err := config.LoadFile()
	if err != nil {
		return nil, err
	}
	if err := a.recordUnsyncedSignIns(st, cfgFile); err != nil {
		return nil, err
	}
	local, err := st.TimedSettingsWithPrefix(ctx, syncedSignInPrefix)
	if err != nil {
		return nil, err
	}
	password := func(k string) string {
		id, found := findSignInBySyncKey(cfgFile, k)
		if !found || id == accountid.Primary {
			return ""
		}
		// 服务器拒绝了的密码不往外传: 它会盖掉别的设备上能用的那个。
		if si, err := a.signInByID(id); err == nil {
			a.mu.RLock()
			rejected := si.reason != ""
			a.mu.RUnlock()
			if rejected {
				return ""
			}
		}
		cfg, _ := cfgFile.SignIn(id)
		pw, _ := config.LoadPassword(cfg.SessionEndpoint, cfg.Username)
		return pw
	}
	plan := planSignInSync(local, remote.SignIns, rec.key, password)

	if plan.write || remote.Encryption == nil || !file.exists() {
		out := signInSyncFile{
			Version: 1, Type: signInSyncFileType, ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Encryption: &rec.Params, SignIns: plan.out,
		}
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := file.write(ctx, body); err != nil {
			return nil, err
		}
	}
	return &plan, nil
}

// recordUnsyncedSignIns 给还没有同步记录的附加登录 (开启同步之前加的) 补一条。时间记成最早:
// 网盘里已有同一个登录时以网盘的为准 (那边的密码可能更新), 本机的只在网盘里没有时传上去。
func (a *App) recordUnsyncedSignIns(st *store.Store, f config.File) error {
	for _, s := range f.SignIns {
		if !s.Config.Configured() {
			continue
		}
		key := syncedSignInPrefix + signInSyncKey(s.Config)
		if _, err := st.StringSetting(a.ctx, key); !errors.Is(err, store.ErrNotFound) {
			continue
		}
		v := store.TimedSetting{Value: syncedSignInOf(s.Config).encode(), UpdatedAt: 1}
		if err := st.WithTx(a.ctx, func(tx *sql.Tx) error { return store.PutTimedSetting(a.ctx, tx, key, v) }); err != nil {
			return err
		}
	}
	return nil
}

// recordSignIn 在添加或重新登录一个附加登录后刷新它的同步记录 (配置或密码可能变了), 并在后台同步。
func (a *App) recordSignIn(id string) {
	if id == accountid.Primary {
		return
	}
	f, err := config.LoadFile()
	if err != nil {
		return
	}
	if cfg, ok := f.SignIn(id); ok && cfg.Configured() {
		a.putSyncedSignIn(signInSyncKey(cfg), syncedSignInOf(cfg).encode())
	}
}

// recordSignInRemoved 在用户移除一个附加登录后留墓碑, 别的设备同步时也移除它。
func (a *App) recordSignInRemoved(cfg config.Config) {
	if cfg.SessionEndpoint != "" && cfg.Username != "" {
		a.putSyncedSignIn(signInSyncKey(cfg), "")
	}
}

// putSyncedSignIn 写一条本机的同步记录。修改时间至少比原来那条晚一秒: 时间按秒记,
// 刚从网盘合并来的记录可能与这次操作在同一秒, 同秒时比内容, 本机的操作就可能输给它覆盖的那一条。
//
// 本机没开启同步时记成最早 (时间 1), 也不触发同步: 这时的添加、移除只影响本机 (移除确认框也这么说),
// 以后开启同步, 网盘里已有的条目照样胜出, 不会拿关着同步时的墓碑去移除别的设备上的登录。
func (a *App) putSyncedSignIn(key, value string) {
	st, err := a.currentStore()
	if err != nil {
		return
	}
	key = syncedSignInPrefix + key
	if rec, err := a.loadSignInSyncKey(); err != nil || rec == nil {
		v := store.TimedSetting{Value: value, UpdatedAt: 1}
		if err := st.WithTx(a.ctx, func(tx *sql.Tx) error { return store.PutTimedSetting(a.ctx, tx, key, v) }); err != nil {
			a.log.Warn("record synced sign-in", "err", err)
		}
		return
	}
	v := store.TimedSetting{Value: value, UpdatedAt: time.Now().Unix()}
	if cur, err := st.TimedSettings(a.ctx, []string{key}); err == nil {
		if c, ok := cur[key]; ok && c.UpdatedAt >= v.UpdatedAt {
			v.UpdatedAt = c.UpdatedAt + 1
		}
	}
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error { return store.PutTimedSetting(a.ctx, tx, key, v) }); err != nil {
		a.log.Warn("record synced sign-in", "err", err)
		return
	}
	a.syncSignInsSoon()
}

// findSignInBySyncKey 找同步键对应的本机登录 (主登录为空串)。
func findSignInBySyncKey(f config.File, key string) (string, bool) {
	if f.Config.Configured() && signInSyncKey(f.Config) == key {
		return accountid.Primary, true
	}
	for _, s := range f.SignIns {
		if s.Config.SessionEndpoint != "" && signInSyncKey(s.Config) == key {
			return s.ID, true
		}
	}
	return "", false
}

// signInSyncAction 是合并后本机要做的事: 加上、移除或更新一个附加登录。
type signInSyncAction struct {
	key     string
	signIn  syncedSignIn
	removed bool
	// password 是解开的应用专用密码, 没有或解不开时为空。
	password string
}

type signInSyncPlan struct {
	// changes 是要写进本机的同步记录 (键不带前缀)。
	changes map[string]store.TimedSetting
	// out 是写回网盘的全部条目; write 为真时需要写。
	out   map[string]signInSyncEntry
	write bool
	// actions 是远端较新的条目, 本机据此加上、移除或更新登录。
	actions []signInSyncAction
}

// planSignInSync 逐条合并本机记录与网盘条目, 较新的一方胜; 时间相同而内容不同时按内容比大小, 两台设备结论一致。
// password 按同步键取本机存的应用专用密码, 用来给网盘条目补上或更新加密的密码。
//
// 网盘里的条目不全信: 配置对不上它的同步键的 (被人改了服务器地址, 想骗设备把密码发过去) 当作不存在;
// 读不懂的 (以后的版本加的登录方式) 原样保留, 不当成移除。只有空条目是墓碑。
func planSignInSync(local map[string]store.TimedSetting, remote map[string]signInSyncEntry, key []byte, password func(string) string) signInSyncPlan {
	plan := signInSyncPlan{changes: map[string]store.TimedSetting{}, out: map[string]signInSyncEntry{}}
	keys := map[string]bool{}
	for k := range local {
		keys[k] = true
	}
	for k := range remote {
		keys[k] = true
	}
	for k := range keys {
		l, inLocal := local[k]
		r, inRemote := remote[k]
		rv := store.TimedSetting{UpdatedAt: parseISO(r.UpdatedAt)}
		if inRemote && r.SignIn != nil {
			rv.Value = r.SignIn.encode()
			s, ok := decodeSyncedSignIn(rv.Value)
			switch {
			case ok && signInSyncKey(s.config()) != k:
				inRemote = false
			case !ok && (!inLocal || rv.UpdatedAt >= l.UpdatedAt):
				plan.out[k] = r
				continue
			case !ok:
				inRemote = false
			}
		}
		if !inLocal && !inRemote {
			plan.write = true
			continue
		}
		fromRemote := inRemote && (!inLocal || rv.UpdatedAt > l.UpdatedAt ||
			(rv.UpdatedAt == l.UpdatedAt && rv.Value >= l.Value))
		winner := l
		if fromRemote {
			winner = rv
			if !inLocal || rv.UpdatedAt != l.UpdatedAt || rv.Value != l.Value {
				plan.changes[k] = rv
				act := signInSyncAction{key: k}
				if s, ok := decodeSyncedSignIn(rv.Value); ok {
					act.signIn = s
					if r.Password != "" {
						act.password, _ = syncseal.Open(key, k, r.Password)
					}
				} else {
					act.removed = true
				}
				plan.actions = append(plan.actions, act)
			}
		} else {
			plan.write = true
		}

		e := signInSyncEntry{UpdatedAt: isoTime(winner.UpdatedAt)}
		s, ok := decodeSyncedSignIn(winner.Value)
		if ok {
			e.SignIn = &s
		}
		if ok && s.AuthMethod == config.AuthPassword {
			// 本机胜出时用本机的密码; 远端胜出但没带密码 (加它的设备没开同步时退出过) 时也补上本机的。
			// 本机没有密码 (退出了、或服务器拒绝了) 时沿用远端的。
			if fromRemote {
				e.Password = r.Password
			}
			if !fromRemote || e.Password == "" {
				if pw := password(k); pw != "" {
					if sealed, err := syncseal.Seal(key, k, pw); err == nil {
						e.Password = sealed
						plan.write = true
					}
				} else if inRemote {
					e.Password = r.Password
				}
			}
		}
		plan.out[k] = e
	}
	slices.SortFunc(plan.actions, func(x, y signInSyncAction) int { return strings.Compare(x.key, y.key) })
	return plan
}

// applySignInSync 按网盘里较新的条目在本机加上、移除或更新一个附加登录。主登录不受影响。
func (a *App) applySignInSync(act signInSyncAction) error {
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	id, found := findSignInBySyncKey(f, act.key)
	if found && id == accountid.Primary {
		return nil
	}
	if act.removed {
		if !found {
			return nil
		}
		a.log.Info("removing sign-in removed on another device", "signin", id)
		err := a.removeSignIn(id)
		if _, e := a.settleSignedOutPrimary(); e != nil {
			err = errors.Join(err, e)
		}
		return err
	}
	if found {
		return a.updateSyncedPassword(id, f, act.password)
	}
	return a.importSignIn(act)
}

// updateSyncedPassword 在别的设备换了某个登录的密码后更新本机存的密码; 这个登录正因凭据被拒停着时用新密码重连。
// 本机退出了的登录不动: 退出是本机的选择。
func (a *App) updateSyncedPassword(id string, f config.File, password string) error {
	cfg, _ := f.SignIn(id)
	if password == "" || cfg.AuthMethod != config.AuthPassword || cfg.SignedOut {
		return nil
	}
	if cur, err := config.LoadPassword(cfg.SessionEndpoint, cfg.Username); err != nil || cur == password {
		return err
	}
	si, err := a.signInByID(id)
	if err != nil {
		return nil
	}
	a.mu.RLock()
	connected := si.client != nil
	idle := !connected && !si.restoring.Load()
	a.mu.RUnlock()
	// 本机的会话正连着 (本机的密码能用): 新密码先向服务器核实, 核实不过不换, 免得拿坏密码盖掉好密码。
	if connected {
		if err := a.probeSession(cfg, basicAuthClient(cfg.Username, password)); err != nil {
			if isCredentialError(err) {
				a.log.Warn("synced password rejected, keeping the local one", "signin", id, "err", err)
				return nil
			}
			// 断网、超时、服务器出错: 这次没核实成, 不算做完, 下一轮再试。
			return err
		}
	}
	if err := config.SavePassword(cfg.SessionEndpoint, cfg.Username, password); err != nil {
		return err
	}
	a.log.Info("password updated from another device", "signin", id)
	if idle {
		go func() {
			if err := a.RetrySignIn(id); err != nil {
				a.log.Warn("reconnect with synced password", "signin", id, "err", err)
			}
			a.emit(EventAccountsChanged)
		}()
	}
	return nil
}

// importSignIn 加上一个别的设备添加的登录。有密码就在后台连接 (同启动时恢复会话, 连不上会退避重试或提示重新登录);
// 浏览器授权的登录与没有密码的加成「已退出」, 在设置 → 账户里点登录。
func (a *App) importSignIn(act signInSyncAction) error {
	cfg := act.signIn.config()
	cfg.SignedOut = cfg.AuthMethod != config.AuthPassword || act.password == ""
	var id string
	if err := config.UpdateFile(func(f *config.File) error {
		if _, found := findSignInBySyncKey(*f, act.key); found {
			return errAlreadySignedIn
		}
		// 查重之后才写密码: 同一个身份刚被手动加上时, 不能拿同步来的密码盖掉用户输入的。
		if !cfg.SignedOut {
			if err := config.SavePassword(cfg.SessionEndpoint, cfg.Username, act.password); err != nil {
				a.log.Warn("save synced password", "err", err)
				cfg.SignedOut = true
			}
		}
		id = f.AddSignIn(cfg)
		return nil
	}); err != nil {
		if errors.Is(err, errAlreadySignedIn) {
			return nil
		}
		return err
	}
	si := &signIn{id: id, cfg: cfg, signedOut: cfg.SignedOut}
	a.mu.Lock()
	a.extra = append(a.extra, si)
	a.mu.Unlock()
	a.log.Info("sign-in added from another device", "signin", id, "connect", !cfg.SignedOut)
	a.emit(EventAccountsChanged)
	if !cfg.SignedOut {
		go func() {
			a.restoreWithRetry(si, cfg)
			a.emit(EventAccountsChanged)
		}()
	}
	return nil
}
