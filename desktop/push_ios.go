//go:build ios

package main

// 新邮件推送 (iOS)。程序退到后台几秒后就被系统挂起, EventSource 断开, 收不到新邮件。
// 改由邮件服务器把新邮件推到 CZL Connect 的推送中转 (文档 connect.czl.net/docs/push), 中转转成 APNs 通知,
// 原生壳的通知扩展解密后显示发件人与主题。
//
// 分工: 原生壳管设备令牌、推送密钥 (P-256 + auth, 存在与通知扩展共享的钥匙串) 与解密;
// 这里管中转登记与 JMAP 推送订阅 (draft-ietf-jmap-emailpush), 内容按 RFC 8291 加密, 中转看不到。
//
// 订阅由 Stalwart 默认 7 天过期, 每次回到前台 (CzlResume) 与每 12 小时续期一次。
// 推送生效后不再由同步弹新邮件通知, 否则前台时同一封信会弹两次。

import (
	"bytes"
	"context"
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

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

const (
	pushRelayURL   = "https://connect.czl.net/api/push"
	pushTopic      = "net.czl.mail.ios"
	pushTTL        = 7 * 24 * time.Hour
	pushRenewEvery = 12 * time.Hour
	pushVerifyWait = 30 * time.Second

	settingPushRelay = "pushRelay" // 中转登记 (pushRelayReg 的 JSON)
)

// 推送推来的邮件属性, 按重要程度排: 放不下时服务端从后往前丢。
var pushEmailProperties = []string{"id", "from", "subject", "preview"}

var push struct {
	mu      sync.Mutex
	wake    chan struct{}
	covered map[string]bool // 推送已覆盖的账号, 这些账号的新邮件不再由同步弹通知
}

func init() { push.wake = make(chan struct{}, 1) }

// pushRelayReg 是在中转登记的订阅。KeyID 是设备推送公钥, 换了密钥 (重装) 就要重建 JMAP 订阅。
type pushRelayReg struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
	URL    string `json:"url"`
	KeyID  string `json:"keyId"`
	SubID  string `json:"subId"` // JMAP PushSubscription id
	// Accounts 是上次对齐成功时推送覆盖的账号。启动时先按它认定推送已生效:
	// 冷启动后的第一轮同步比对齐早, 不这样会把推送已经通知过的新信再弹一次。
	Accounts []string `json:"accounts,omitempty"`
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
	return push.covered[accountID]
}

func setPushCovered(ids []string) {
	push.mu.Lock()
	defer push.mu.Unlock()
	push.covered = map[string]bool{}
	for _, id := range ids {
		push.covered[id] = true
	}
}

// wakePush 让推送循环马上重新对齐一次 (设置变了、回到前台、令牌变了)。
func (a *App) wakePush() {
	select {
	case push.wake <- struct{}{}:
	default:
	}
}

func (a *App) pushSettingsChanged() { a.wakePush() }

// runPush 跟随一次会话运行: 启动时、每 12 小时、被唤醒时对齐推送订阅。
// primePush 在同步开始前恢复「推送已生效」的标记 (见 pushRelayReg.Accounts), 同步调用。
func (a *App) primePush(ctx context.Context, st *store.Store) {
	if reg := loadPushReg(ctx, st); reg.SubID != "" {
		setPushCovered(reg.Accounts)
	}
}

func (a *App) runPush(ctx context.Context, s *syncer.Syncer, st *store.Store) {
	// 让首次同步先跑, 推送晚几秒没关系。
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	t := time.NewTicker(pushRenewEvery)
	defer t.Stop()
	for {
		if err := a.syncPush(ctx, s, st); err != nil && ctx.Err() == nil {
			a.log.Warn("push subscription", "err", err)
		}
		select {
		case <-ctx.Done():
			setPushCovered(nil)
			return
		case <-t.C:
		case <-push.wake:
		}
	}
}

// syncPush 让服务端的推送订阅与当前设置一致。
func (a *App) syncPush(ctx context.Context, s *syncer.Syncer, st *store.Store) error {
	reg := loadPushReg(ctx, st)
	accounts, filters := a.pushTargets(ctx, s, st)
	if len(accounts) == 0 {
		a.log.Info("push off: no account to push (notifications off, all muted, or server lacks emailpush)")
		setPushCovered(nil)
		return a.dropPush(ctx, s, st, reg)
	}

	raw, err := hostRequest(ctx, "pushInfo", struct{}{})
	if err != nil {
		// 没有通知权限或拿不到设备令牌: 退回同步时弹通知。
		setPushCovered(nil)
		return fmt.Errorf("2290 push unavailable: %w", err)
	}
	var info pushInfo
	if err := json.Unmarshal(raw, &info); err != nil || info.Token == "" || info.P256DH == "" {
		setPushCovered(nil)
		return fmt.Errorf("2290 push unavailable: bad device info")
	}

	if reg, err = a.ensureRelay(ctx, st, reg, info); err != nil {
		setPushCovered(nil)
		return err
	}

	emailPush := map[string]any{}
	for _, id := range accounts {
		emailPush[id] = syncer.EmailPushConfig{Filter: filters[id], Properties: pushEmailProperties, Urgency: "high"}
	}
	expires := time.Now().Add(pushTTL).UTC().Format(time.RFC3339)

	subs, err := s.PushSubscriptions(ctx)
	if err != nil {
		return err
	}
	current := slices.IndexFunc(subs, func(p syncer.PushSubscription) bool { return p.ID == reg.SubID })
	if current >= 0 && reg.KeyID == info.P256DH {
		if _, err := s.SetPushSubscription(ctx, nil, map[string]map[string]any{
			reg.SubID: {"emailPush": emailPush, "expires": expires},
		}, nil); err != nil {
			return err
		}
		reg.Accounts = accounts
		savePushReg(ctx, st, reg)
		setPushCovered(accounts)
		return nil
	}

	// 新建: 先清掉本设备留下的旧订阅 (重装、换密钥、中转登记丢失), 免得同一封信推两次。
	var stale []string
	for _, p := range subs {
		if p.DeviceClientID == info.DeviceID {
			stale = append(stale, p.ID)
		}
	}
	if len(stale) > 0 {
		if _, err := s.SetPushSubscription(ctx, nil, nil, stale); err != nil {
			a.log.Warn("drop stale push subscriptions", "err", err)
		}
	}
	subID, err := s.SetPushSubscription(ctx, map[string]any{
		"deviceClientId": info.DeviceID,
		"url":            reg.URL,
		"keys":           map[string]string{"p256dh": info.P256DH, "auth": info.Auth},
		"types":          []string{},
		"expires":        expires,
		"emailPush":      emailPush,
	}, nil, nil)
	if err != nil {
		return err
	}
	reg.SubID, reg.KeyID = subID, info.P256DH
	savePushReg(ctx, st, reg)

	code, err := a.awaitVerification(ctx, reg, subID)
	if err != nil {
		return err
	}
	if _, err := s.SetPushSubscription(ctx, nil, map[string]map[string]any{subID: {"verificationCode": code}}, nil); err != nil {
		return err
	}
	if err := relayCall(ctx, http.MethodPost, "/v1/subscriptions/"+reg.ID+"/activate", reg.Secret, nil, nil); err != nil {
		return err
	}
	a.log.Info("push subscription active", "accounts", len(accounts))
	reg.Accounts = accounts
	savePushReg(ctx, st, reg)
	setPushCovered(accounts)
	return nil
}

// pushTargets 选出要推送的账号与各自的过滤条件: 开了新邮件通知、没静音、服务端支持的账号的收件箱,
// 排除自己任一发件身份发出的信 (与同步时的通知规则一致)。
func (a *App) pushTargets(ctx context.Context, s *syncer.Syncer, st *store.Store) ([]string, map[string]any) {
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
		if a.mailMuted(acc.ID) || !s.SupportsEmailPush(acc.ID) {
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

// ensureRelay 保证中转里有本设备的登记, 并把最新的设备令牌带过去。
func (a *App) ensureRelay(ctx context.Context, st *store.Store, reg pushRelayReg, info pushInfo) (pushRelayReg, error) {
	body := map[string]any{
		"topic": pushTopic, "token": info.Token, "sandbox": info.Sandbox,
		"alert": map[string]string{"title": "CZL Mail", "body": "新邮件"},
	}
	if reg.ID != "" {
		err := relayCall(ctx, http.MethodPut, "/v1/subscriptions/"+reg.ID, reg.Secret, body, nil)
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
	if err := relayCall(ctx, http.MethodPost, "/v1/subscriptions", "", body, &created); err != nil {
		return reg, err
	}
	reg = pushRelayReg{ID: created.ID, Secret: created.Secret, URL: created.URL}
	savePushReg(ctx, st, reg)
	return reg, nil
}

// awaitVerification 从中转取服务端推来的 PushVerification (同样加密, 交原生壳解密), 返回验证码。
func (a *App) awaitVerification(ctx context.Context, reg pushRelayReg, subID string) (string, error) {
	deadline := time.Now().Add(pushVerifyWait)
	for time.Now().Before(deadline) {
		var pending struct {
			Messages []string `json:"messages"`
		}
		if err := relayCall(ctx, http.MethodGet, "/v1/subscriptions/"+reg.ID+"/pending", reg.Secret, nil, &pending); err != nil {
			return "", err
		}
		for _, m := range pending.Messages {
			plain, err := hostRequest(ctx, "pushDecrypt", map[string]string{"body": m})
			if err != nil {
				continue
			}
			var text string
			if json.Unmarshal(plain, &text) != nil {
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
		case <-time.After(time.Second):
		}
	}
	return "", fmt.Errorf("2291 push verification did not arrive")
}

// dropPush 撤掉推送 (关了通知或全部静音): 删 JMAP 订阅与中转登记。
func (a *App) dropPush(ctx context.Context, s *syncer.Syncer, st *store.Store, reg pushRelayReg) error {
	if reg.ID == "" {
		return nil
	}
	if reg.SubID != "" {
		if _, err := s.SetPushSubscription(ctx, nil, nil, []string{reg.SubID}); err != nil {
			a.log.Warn("destroy push subscription", "err", err)
		}
	}
	if err := relayCall(ctx, http.MethodDelete, "/v1/subscriptions/"+reg.ID, reg.Secret, nil, nil); err != nil && !errors.Is(err, errRelayNotFound) {
		return err
	}
	savePushReg(ctx, st, pushRelayReg{})
	return nil
}

func loadPushReg(ctx context.Context, st *store.Store) pushRelayReg {
	var reg pushRelayReg
	if raw, err := st.StringSetting(ctx, settingPushRelay); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &reg)
	}
	return reg
}

func savePushReg(ctx context.Context, st *store.Store, reg pushRelayReg) {
	raw := ""
	if reg.ID != "" {
		b, _ := json.Marshal(reg)
		raw = string(b)
	}
	_ = st.SetStringSetting(ctx, settingPushRelay, raw)
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
