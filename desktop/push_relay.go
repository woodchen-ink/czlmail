package main

// 新邮件推送的平台无关部分: 中转登记、JMAP 推送订阅的建立 / 续期 / 撤销、「推送已覆盖」标记。
// 只有 iOS 真正运行它 (push_ios.go 接上原生壳与推送循环); 放在不带构建标签的文件里, 桌面上也能跑单元测试。
//
// 每个登录 (#15) 各有一份: 推送订阅属于一个 JMAP 会话, 中转登记也按登录分开。
// 设备的 P-256 密钥所有登录共用; auth 按登录派生 (pushAuthFor): 中转发到设备的通知里只有密文,
// 推送内容里只有服务器上的账号 id (两台服务器的账号 id 常常相同), 通知扩展按哪一把 auth 解得开认出是哪个登录,
// 再拼出带前缀的本机账号 id 作为 actionId。主登录用设备原来的 auth, 与旧版的订阅兼容。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

const (
	pushRelayURL   = "https://connect.czl.net/api/push"
	pushTopic      = "net.czl.mail.ios"
	pushTTL        = 7 * 24 * time.Hour
	pushRenewEvery = 12 * time.Hour
	pushVerifyWait = 30 * time.Second

	settingPushRelay = "pushRelay" // 各登录的中转登记 (pushRelayState 的 JSON)
)

// 推送推来的邮件属性, 按重要程度排: 放不下时服务端从后往前丢。
var pushEmailProperties = []string{"id", "from", "subject", "preview"}

var push struct {
	mu   sync.Mutex
	wake chan struct{}
	// covered 是各登录推送已覆盖的账号 (本机账号 id), 这些账号的新邮件不再由同步弹通知。
	covered map[string][]string
	// store 串行化 pushRelay 设置的读改写: 各登录的推送循环并行运行。
	store sync.Mutex
}

func init() { push.wake = make(chan struct{}, 1) }

// pushHooks 是推送用到的外部依赖: 原生壳 (设备信息、解密) 与中转。iOS 上接真实实现 (push_ios.go), 测试里换成假的。
type pushHooks struct {
	deviceInfo func(ctx context.Context) (pushInfo, error)
	// decrypt 用这个登录的 auth (pushAuthFor) 解密中转暂存的消息。
	decrypt func(ctx context.Context, body, signIn string) (string, error)
	// signIns 告诉原生壳哪些附加登录有推送, 通知扩展据此逐个试派生的 auth。旧版原生壳不认这个请求, 错误忽略。
	signIns func(ctx context.Context, prefixes []string) error
	relay   func(ctx context.Context, method, path, secret string, in, out any) error
	// verifyPoll 是等 PushVerification 时两次查询的间隔。
	verifyPoll time.Duration
}

var pushDeps = pushHooks{relay: relayCall, verifyPoll: time.Second}

// pushSession 是推送用到的同步器方法 (syncer.Syncer 实现)。
type pushSession interface {
	ServerID(local string) string
	Owns(local string) bool
	SupportsEmailPush(local string) bool
	PushSubscriptions(ctx context.Context) ([]syncer.PushSubscription, error)
	SetPushSubscription(ctx context.Context, create map[string]any, update map[string]map[string]any, destroy []string) (string, error)
}

// pushRelayReg 是一个登录在中转的登记。KeyID 是设备推送公钥, 换了密钥 (重装) 就要重建 JMAP 订阅。
type pushRelayReg struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
	URL    string `json:"url"`
	KeyID  string `json:"keyId"`
	SubID  string `json:"subId"` // JMAP PushSubscription id
	// Accounts 是上次对齐成功时推送覆盖的账号 (本机账号 id)。启动时先按它认定推送已生效:
	// 冷启动后的第一轮同步比对齐早, 不这样会把推送已经通知过的新信再弹一次。
	Accounts []string `json:"accounts,omitempty"`
}

// pushRelayState 是 pushRelay 设置的内容: 登录前缀 → 登记。主登录的键是空串。
// 旧版只有主登录, 存的是一个 pushRelayReg 本身, 读的时候认作主登录的登记。
type pushRelayState struct {
	SignIns map[string]pushRelayReg `json:"signIns"`
}

// pushInfo 是原生壳给的设备信息。
type pushInfo struct {
	Token    string `json:"token"`
	Sandbox  bool   `json:"sandbox"`
	P256DH   string `json:"p256dh"`
	Auth     string `json:"auth"`
	DeviceID string `json:"deviceId"`
}

// pushCovers 报告某账号的新邮件是否已由推送通知。
func (a *App) pushCovers(accountID string) bool {
	push.mu.Lock()
	defer push.mu.Unlock()
	prefix, _ := accountid.Split(accountID)
	return slices.Contains(push.covered[prefix], accountID)
}

// setPushCovered 记下某个登录推送覆盖的账号; ids 为空表示这个登录没有推送。
func setPushCovered(signIn string, ids []string) {
	push.mu.Lock()
	defer push.mu.Unlock()
	if push.covered == nil {
		push.covered = map[string][]string{}
	}
	if len(ids) == 0 {
		delete(push.covered, signIn)
		return
	}
	push.covered[signIn] = slices.Clone(ids)
}

// wakePush 让各登录的推送循环马上重新对齐一次 (设置变了、回到前台、令牌变了)。
// 循环有好几个, 都要收到: 关掉通道再换新的, 每个循环都在等的那个通道就被唤醒了。
func (a *App) wakePush() {
	push.mu.Lock()
	defer push.mu.Unlock()
	close(push.wake)
	push.wake = make(chan struct{})
}

// pushWake 返回当前的唤醒通道, 推送循环每轮等它一次。
func pushWake() <-chan struct{} {
	push.mu.Lock()
	defer push.mu.Unlock()
	return push.wake
}

func (a *App) pushSettingsChanged() { a.wakePush() }

// primePush 在同步开始前恢复这个登录「推送已生效」的标记 (见 pushRelayReg.Accounts), 同步调用。
func (a *App) primePush(ctx context.Context, signIn string, st *store.Store) {
	if !pushEnabled {
		return
	}
	if reg := loadPushReg(ctx, st, signIn); reg.SubID != "" {
		setPushCovered(signIn, reg.Accounts)
	}
}

// pushLoop 跟随一个登录的会话运行: 启动时、每 12 小时、被唤醒时对齐推送订阅。
func (a *App) pushLoop(ctx context.Context, signIn string, s pushSession, st *store.Store) {
	// 让首次同步先跑, 推送晚几秒没关系。
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	t := time.NewTicker(pushRenewEvery)
	defer t.Stop()
	for {
		wake := pushWake()
		if err := a.syncPush(ctx, signIn, s, st); err != nil && ctx.Err() == nil {
			a.log.Warn("push subscription", "signin", signIn, "err", err)
		}
		select {
		case <-ctx.Done():
			setPushCovered(signIn, nil)
			return
		case <-t.C:
		case <-wake:
		}
	}
}

// syncPush 让这个登录在服务端的推送订阅与当前设置一致。
func (a *App) syncPush(ctx context.Context, signIn string, s pushSession, st *store.Store) error {
	reg := loadPushReg(ctx, st, signIn)
	accounts, filters := a.pushTargets(ctx, s, st)
	if len(accounts) == 0 {
		a.log.Info("push off: no account to push (notifications off, all muted, or server lacks emailpush)", "signin", signIn)
		setPushCovered(signIn, nil)
		return a.dropPush(ctx, signIn, s, st, reg)
	}

	if pushDeps.deviceInfo == nil {
		setPushCovered(signIn, nil)
		return fmt.Errorf("2290 push unavailable: no native push support")
	}
	info, err := pushDeps.deviceInfo(ctx)
	if err != nil {
		// 没有通知权限或拿不到设备令牌: 退回同步时弹通知。
		setPushCovered(signIn, nil)
		return fmt.Errorf("2290 push unavailable: %w", err)
	}
	if info.Token == "" || info.P256DH == "" {
		setPushCovered(signIn, nil)
		return fmt.Errorf("2290 push unavailable: bad device info")
	}

	if reg, err = a.ensureRelay(ctx, signIn, st, reg, info); err != nil {
		setPushCovered(signIn, nil)
		return err
	}

	emailPush := map[string]any{}
	for _, id := range accounts {
		emailPush[s.ServerID(id)] = syncer.EmailPushConfig{Filter: filters[id], Properties: pushEmailProperties, Urgency: "high"}
	}
	expires := time.Now().Add(pushTTL).UTC().Format(time.RFC3339)

	auth, err := pushAuthFor(info.Auth, signIn)
	if err != nil {
		setPushCovered(signIn, nil)
		return err
	}

	subs, err := s.PushSubscriptions(ctx)
	if err != nil {
		return err
	}
	current := slices.IndexFunc(subs, func(p syncer.PushSubscription) bool { return p.ID == reg.SubID })
	if current >= 0 && reg.KeyID == info.P256DH {
		// 续期: 订阅还在, 设备密钥没换, 更新覆盖的账号与过期时间。
		if _, err := s.SetPushSubscription(ctx, nil, map[string]map[string]any{
			reg.SubID: {"emailPush": emailPush, "expires": expires},
		}, nil); err != nil {
			return err
		}
		reg.Accounts = accounts
		savePushReg(ctx, st, signIn, reg)
		setPushCovered(signIn, accounts)
		return nil
	}

	// 新建: 先清掉本设备在这个会话里留下的旧订阅 (重装、换密钥、中转登记丢失), 免得同一封信推两次。
	var stale []string
	for _, p := range subs {
		if p.DeviceClientID == info.DeviceID {
			stale = append(stale, p.ID)
		}
	}
	if len(stale) > 0 {
		if _, err := s.SetPushSubscription(ctx, nil, nil, stale); err != nil {
			a.log.Warn("drop stale push subscriptions", "signin", signIn, "err", err)
		}
	}
	subID, err := s.SetPushSubscription(ctx, map[string]any{
		"deviceClientId": info.DeviceID,
		"url":            reg.URL,
		"keys":           map[string]string{"p256dh": info.P256DH, "auth": auth},
		"types":          []string{},
		"expires":        expires,
		"emailPush":      emailPush,
	}, nil, nil)
	if err != nil {
		return err
	}
	reg.SubID, reg.KeyID, reg.Accounts = subID, info.P256DH, nil
	savePushReg(ctx, st, signIn, reg)

	code, err := a.awaitVerification(ctx, signIn, reg, subID)
	if err != nil {
		return err
	}
	if _, err := s.SetPushSubscription(ctx, nil, map[string]map[string]any{subID: {"verificationCode": code}}, nil); err != nil {
		return err
	}
	if err := pushDeps.relay(ctx, http.MethodPost, "/v1/subscriptions/"+reg.ID+"/activate", reg.Secret, nil, nil); err != nil {
		return err
	}
	a.log.Info("push subscription active", "signin", signIn, "accounts", len(accounts))
	reg.Accounts = accounts
	savePushReg(ctx, st, signIn, reg)
	setPushCovered(signIn, accounts)
	return nil
}

// pushTargets 选出这个会话要推送的账号与各自的过滤条件: 开了新邮件通知、没静音、服务端支持的账号的收件箱,
// 排除自己任一地址 (所有登录的) 发出的信, 与同步时的通知规则一致。
func (a *App) pushTargets(ctx context.Context, s pushSession, st *store.Store) ([]string, map[string]any) {
	if !a.settingOn("notifyMail", true) {
		return nil, nil
	}
	list, err := st.Accounts(ctx)
	if err != nil {
		return nil, nil
	}
	own := make([]string, 0)
	for addr := range a.ownAddresses() {
		own = append(own, addr)
	}
	sort.Strings(own)

	var ids []string
	filters := map[string]any{}
	for _, acc := range list {
		// 推送订阅属于一个会话, 只登记这个会话自己的账号。
		if !s.Owns(acc.ID) || a.mailMuted(acc.ID) || !s.SupportsEmailPush(acc.ID) {
			continue
		}
		inbox, err := mailboxByRole(st, ctx, acc.ID, "inbox")
		if err != nil || inbox == "" {
			continue
		}
		var filter any = map[string]any{"inMailbox": inbox}
		if len(own) > 0 {
			notMine := make([]any, 0, len(own))
			for _, addr := range own {
				notMine = append(notMine, map[string]any{"from": addr})
			}
			filter = map[string]any{"operator": "AND", "conditions": []any{
				filter,
				map[string]any{"operator": "NOT", "conditions": notMine},
			}}
		}
		ids = append(ids, acc.ID)
		filters[acc.ID] = filter
	}
	return ids, filters
}

// ensureRelay 保证中转里有这个登录的登记, 并把最新的设备令牌带过去。
func (a *App) ensureRelay(ctx context.Context, signIn string, st *store.Store, reg pushRelayReg, info pushInfo) (pushRelayReg, error) {
	body := map[string]any{
		"topic": pushTopic, "token": info.Token, "sandbox": info.Sandbox,
		// 占位文字: 扩展解不开密文时原样显示; 每次续期都会带上, 换语言后下次续期生效。
		"alert": map[string]string{"title": "CZL Mail", "body": a.t("新邮件")},
	}
	if reg.ID != "" {
		err := pushDeps.relay(ctx, http.MethodPut, "/v1/subscriptions/"+reg.ID, reg.Secret, body, nil)
		if err == nil {
			return reg, nil
		}
		if !errors.Is(err, errRelayNotFound) {
			return reg, err
		}
		// 中转那边没有了 (两个月没刷新被清理, 或令牌失效被删), 重新登记; 旧 JMAP 订阅指向的地址已失效。
	}
	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
		URL    string `json:"url"`
	}
	if err := pushDeps.relay(ctx, http.MethodPost, "/v1/subscriptions", "", body, &created); err != nil {
		return reg, err
	}
	reg = pushRelayReg{ID: created.ID, Secret: created.Secret, URL: created.URL}
	savePushReg(ctx, st, signIn, reg)
	return reg, nil
}

// awaitVerification 从中转取服务端推来的 PushVerification (同样加密, 交原生壳解密), 返回验证码。
func (a *App) awaitVerification(ctx context.Context, signIn string, reg pushRelayReg, subID string) (string, error) {
	deadline := time.Now().Add(pushVerifyWait)
	for time.Now().Before(deadline) {
		var pending struct {
			Messages []string `json:"messages"`
		}
		if err := pushDeps.relay(ctx, http.MethodGet, "/v1/subscriptions/"+reg.ID+"/pending", reg.Secret, nil, &pending); err != nil {
			return "", err
		}
		for _, m := range pending.Messages {
			if pushDeps.decrypt == nil {
				break
			}
			text, err := pushDeps.decrypt(ctx, m, signIn)
			if err != nil {
				continue
			}
			var v struct {
				Type  string `json:"@type"`
				SubID string `json:"pushSubscriptionId"`
				Code  string `json:"verificationCode"`
			}
			if json.Unmarshal([]byte(text), &v) == nil && v.Type == "PushVerification" && v.SubID == subID {
				return v.Code, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pushDeps.verifyPoll):
		}
	}
	return "", fmt.Errorf("2291 push verification did not arrive")
}

// dropPush 撤掉这个登录的推送 (关了通知、全部静音、退出登录): 删 JMAP 订阅与中转登记。s 为空时只删中转登记。
func (a *App) dropPush(ctx context.Context, signIn string, s pushSession, st *store.Store, reg pushRelayReg) error {
	if reg.ID == "" {
		return nil
	}
	if reg.SubID != "" && s != nil {
		if _, err := s.SetPushSubscription(ctx, nil, nil, []string{reg.SubID}); err != nil {
			a.log.Warn("destroy push subscription", "signin", signIn, "err", err)
		}
	}
	if err := pushDeps.relay(ctx, http.MethodDelete, "/v1/subscriptions/"+reg.ID, reg.Secret, nil, nil); err != nil && !errors.Is(err, errRelayNotFound) {
		return err
	}
	savePushReg(ctx, st, signIn, pushRelayReg{})
	return nil
}

// stopPush 在退出或移除一个登录时撤掉它的推送, 否则这台手机仍会收到该登录的新邮件通知。
// 会话还在时连服务器上的订阅一起删; 会话已经没了 (离线、凭据失效) 只删中转登记,
// 中转回 404 后服务器会自己停用那个推送地址。
func (a *App) stopPush(signIn string) {
	if !pushEnabled {
		return
	}
	a.mu.RLock()
	var s pushSession
	if si := a.signInLocked(signIn); si != nil && si.sync != nil {
		s = si.sync
	}
	st := a.store
	a.mu.RUnlock()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	setPushCovered(signIn, nil)
	if err := a.dropPush(ctx, signIn, s, st, loadPushReg(ctx, st, signIn)); err != nil {
		a.log.Warn("drop push", "signin", signIn, "err", err)
		// 中转暂时连不上: 登记也不留, 下次登录会重新登记; 留着的话这台手机会一直收到推送。
		savePushReg(ctx, st, signIn, pushRelayReg{})
	}
}

// forgetPush 在主登录换人、清缓存前删掉上一个身份在中转的登记: 旧会话已经没了, 服务器上的订阅删不掉,
// 但中转回 404 后服务器会自己停用那个推送地址, 上一个人的新邮件不再推到这台手机。别的登录的登记不动。
func (a *App) forgetPush(ctx context.Context, st *store.Store) {
	if !pushEnabled {
		return
	}
	reg := loadPushReg(ctx, st, accountid.Primary)
	setPushCovered(accountid.Primary, nil)
	if reg.ID == "" {
		return
	}
	if err := pushDeps.relay(ctx, http.MethodDelete, "/v1/subscriptions/"+reg.ID, reg.Secret, nil, nil); err != nil && !errors.Is(err, errRelayNotFound) {
		a.log.Warn("forget previous push registration", "err", err)
	}
	savePushReg(ctx, st, accountid.Primary, pushRelayReg{})
}

// pushAuthLabel 是派生附加登录 auth 的 HMAC 消息前缀, 与原生壳 (CzlPush/PushKeys.swift 的 authFor) 一致, 不能改。
const pushAuthLabel = "czlmail push auth "

// pushAuthFor 返回这个登录的订阅用的 auth (base64url 无填充): 主登录用设备的 auth;
// 附加登录用 HMAC-SHA256(设备 auth, pushAuthLabel + 前缀) 的前 16 字节。设备 auth 不出本机, 派生值服务器与中转都推不出别的登录的。
func pushAuthFor(deviceAuth, signIn string) (string, error) {
	if signIn == accountid.Primary {
		return deviceAuth, nil
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(deviceAuth, "="))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("2290 push unavailable: bad device auth")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(pushAuthLabel + signIn))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16]), nil
}

func loadPushRegs(ctx context.Context, st *store.Store) map[string]pushRelayReg {
	raw, err := st.StringSetting(ctx, settingPushRelay)
	if err != nil || raw == "" {
		return map[string]pushRelayReg{}
	}
	var state pushRelayState
	if json.Unmarshal([]byte(raw), &state) == nil && state.SignIns != nil {
		return state.SignIns
	}
	// 旧版的单份登记: 属于主登录。
	var old pushRelayReg
	if json.Unmarshal([]byte(raw), &old) == nil && old.ID != "" {
		return map[string]pushRelayReg{accountid.Primary: old}
	}
	return map[string]pushRelayReg{}
}

func loadPushReg(ctx context.Context, st *store.Store, signIn string) pushRelayReg {
	push.store.Lock()
	defer push.store.Unlock()
	return loadPushRegs(ctx, st)[signIn]
}

// savePushReg 写回一个登录的登记; reg.ID 为空表示删掉这个登录的登记。
func savePushReg(ctx context.Context, st *store.Store, signIn string, reg pushRelayReg) {
	push.store.Lock()
	defer push.store.Unlock()
	regs := loadPushRegs(ctx, st)
	if reg.ID == "" {
		delete(regs, signIn)
	} else {
		regs[signIn] = reg
	}
	raw := ""
	if len(regs) > 0 {
		b, _ := json.Marshal(pushRelayState{SignIns: regs})
		raw = string(b)
	}
	_ = st.SetStringSetting(ctx, settingPushRelay, raw)
	if pushDeps.signIns != nil {
		prefixes := []string{}
		for id := range regs {
			if id != accountid.Primary {
				prefixes = append(prefixes, id)
			}
		}
		sort.Strings(prefixes)
		_ = pushDeps.signIns(ctx, prefixes)
	}
}

var errRelayNotFound = errors.New("push relay subscription not found")

var relayClient = &http.Client{Timeout: 15 * time.Second}

// relayCall 调中转接口。secret 非空时带 Bearer; out 非空时解析 JSON 响应。
func relayCall(ctx context.Context, method, path, secret string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, pushRelayURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := relayClient.Do(req)
	if err != nil {
		return fmt.Errorf("2292 push relay unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errRelayNotFound
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("2293 push relay %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
