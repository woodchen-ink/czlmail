package main

import (
	"database/sql"
	"encoding/json"
	"sort"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 远程内容的信任管理。
//
// 默认拦截一切远程图片; 只有显式信任的发件人, 或(开关开启时)通讯录里的人,
// 才会在打开邮件时直接加载。

// IsSenderTrusted 判断该发件人的远程内容是否可直接加载。
func (a *App) IsSenderTrusted(accountID, address string) (bool, error) {
	st, err := a.currentStore()
	if err != nil {
		return false, err
	}
	return st.IsSenderTrusted(a.ctx, accountID, address)
}

// TrustSender 永久信任该发件人。
func (a *App) TrustSender(accountID, address string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		return store.TrustSender(a.ctx, tx, accountID, address)
	}); err != nil {
		return err
	}
	// 本地先生效, 服务端信任通讯录在后台写入: 离线时也能立刻看到图片。
	go a.syncTrustToServer(address, true)
	return nil
}

// UntrustSender 撤销对该发件人的信任。
func (a *App) UntrustSender(accountID, address string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		return store.UntrustSender(a.ctx, tx, accountID, address)
	}); err != nil {
		return err
	}
	a.syncTrustToServer(address, false)
	return nil
}

// ListTrustedSenders 列出信任名单, 供设置页管理与撤销。
func (a *App) ListTrustedSenders(accountID string) ([]string, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	local, err := st.TrustedSenders(a.ctx, accountID)
	if err != nil {
		return nil, err
	}
	remote, err := st.TrustedBookEntries(a.ctx, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, addr := range local {
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	for _, e := range remote {
		if !seen[e.Address] {
			seen[e.Address] = true
			out = append(out, e.Address)
		}
	}
	sort.Strings(out)
	return out, nil
}

// syncTrustToServer 把信任变更写进个人账号的「Trusted Senders」通讯录, 与 Bulwark 共用。
// 通讯录不存在时新建。失败只记日志: 本地名单已经生效。
func (a *App) syncTrustToServer(address string, trusted bool) {
	s, err := a.currentSyncer()
	if err != nil {
		return
	}
	st, err := a.currentStore()
	if err != nil {
		return
	}
	addr := store.NormalizeAddress(address)

	if !trusted {
		entries, err := st.TrustedBookEntries(a.ctx, addr)
		if err != nil {
			return
		}
		byAccount := map[string][]string{}
		for _, e := range entries {
			byAccount[e.AccountID] = append(byAccount[e.AccountID], e.CardID)
		}
		for acc, ids := range byAccount {
			if _, err := s.SetPIM(a.ctx, acc, jmapx.ContactCard, nil, nil, ids, nil); err != nil {
				a.log.Warn("untrust on server", "address", addr, "err", err)
			}
		}
		return
	}

	if entries, err := st.TrustedBookEntries(a.ctx, addr); err != nil || len(entries) > 0 {
		return
	}
	accountID := a.personalAccountID(st)
	if accountID == "" {
		return
	}
	book, err := st.AddressBookByName(a.ctx, accountID, store.TrustedSendersBook)
	bookID := ""
	if err == nil {
		bookID = book.ID
	} else {
		created, err := s.SetPIM(a.ctx, accountID, jmapx.AddressBook,
			map[string]any{"book": map[string]any{"name": store.TrustedSendersBook, "isSubscribed": false}}, nil, nil, nil)
		if err != nil {
			a.log.Warn("create trusted senders book", "err", err)
			return
		}
		bookID = created["book"]
	}
	card := map[string]any{
		"@type": "Card", "version": "1.0", "uid": "urn:uuid:" + newUUID(),
		"addressBookIds": map[string]bool{bookID: true},
		"emails":         map[string]any{"email": map[string]any{"@type": "EmailAddress", "address": addr}},
	}
	if _, err := s.SetPIM(a.ctx, accountID, jmapx.ContactCard, map[string]any{"card": card}, nil, nil, nil); err != nil {
		a.log.Warn("trust on server", "address", addr, "err", err)
	}
}

func (a *App) personalAccountID(st *store.Store) string {
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return ""
	}
	for _, acc := range accounts {
		if acc.IsPersonal {
			return acc.ID
		}
	}
	return ""
}

// AppSettings 是界面可调的偏好, 存本地缓存库。
type AppSettings struct {
	// TrustAddressBookSenders 为真时, 通讯录里的发件人自动放行远程内容。
	TrustAddressBookSenders bool `json:"trustAddressBookSenders"`
	// SenderAvatars 为真时显示发件人头像。
	SenderAvatars bool `json:"senderAvatars"`
	// NotifyMail / NotifyEvents 控制新邮件通知与日程提醒。
	NotifyMail   bool `json:"notifyMail"`
	NotifyEvents bool `json:"notifyEvents"`
	// MarkRead 是打开邮件后标记已读的时机: immediate / delay / never。
	MarkRead string `json:"markRead"`
	// SignatureSeparator 为真时在签名前插入 RFC 3676 的 "-- " 分隔行。
	SignatureSeparator bool `json:"signatureSeparator"`
	// ReadReceiptDefault 写信时默认请求已读回执。
	ReadReceiptDefault bool `json:"readReceiptDefault"`
	// WarnEmptySubject / WarnMissingAttachment 发送前的检查。
	WarnEmptySubject      bool `json:"warnEmptySubject"`
	WarnMissingAttachment bool `json:"warnMissingAttachment"`
	// DefaultIdentities 是 账号 id → 默认发件身份 id。
	DefaultIdentities map[string]string `json:"defaultIdentities"`
	// MutedMailAccounts 是关闭了新邮件通知的账号(多为共享邮箱)。
	MutedMailAccounts []string `json:"mutedMailAccounts"`
}

var boolSettings = []struct {
	key string
	def bool
	get func(*AppSettings) *bool
}{
	{store.SettingTrustAddressBook, true, func(s *AppSettings) *bool { return &s.TrustAddressBookSenders }},
	{store.SettingSenderAvatars, true, func(s *AppSettings) *bool { return &s.SenderAvatars }},
	{"notifyMail", true, func(s *AppSettings) *bool { return &s.NotifyMail }},
	{"notifyEvents", true, func(s *AppSettings) *bool { return &s.NotifyEvents }},
	{"signatureSeparator", true, func(s *AppSettings) *bool { return &s.SignatureSeparator }},
	{"readReceiptDefault", false, func(s *AppSettings) *bool { return &s.ReadReceiptDefault }},
	{"warnEmptySubject", true, func(s *AppSettings) *bool { return &s.WarnEmptySubject }},
	{"warnMissingAttachment", true, func(s *AppSettings) *bool { return &s.WarnMissingAttachment }},
}

func (a *App) GetSettings() (AppSettings, error) {
	var s AppSettings
	st, err := a.currentStore()
	if err != nil {
		return s, err
	}
	for _, b := range boolSettings {
		v, err := st.BoolSetting(a.ctx, b.key, b.def)
		if err != nil {
			return s, err
		}
		*b.get(&s) = v
	}
	s.MarkRead = "immediate"
	if v, err := st.StringSetting(a.ctx, "markRead"); err == nil && v != "" {
		s.MarkRead = v
	}
	s.DefaultIdentities = map[string]string{}
	if v, err := st.StringSetting(a.ctx, "defaultIdentities"); err == nil {
		_ = json.Unmarshal([]byte(v), &s.DefaultIdentities)
	}
	s.MutedMailAccounts = []string{}
	if v, err := st.StringSetting(a.ctx, "mutedMailAccounts"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s.MutedMailAccounts)
	}
	return s, nil
}

func (a *App) SaveSettings(s AppSettings) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		for _, b := range boolSettings {
			if err := store.SetBoolSetting(a.ctx, tx, b.key, *b.get(&s)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	switch s.MarkRead {
	case "immediate", "delay", "never":
	default:
		s.MarkRead = "immediate"
	}
	if err := st.SetStringSetting(a.ctx, "markRead", s.MarkRead); err != nil {
		return err
	}
	ids, _ := json.Marshal(s.DefaultIdentities)
	if err := st.SetStringSetting(a.ctx, "defaultIdentities", string(ids)); err != nil {
		return err
	}
	if s.MutedMailAccounts == nil {
		s.MutedMailAccounts = []string{}
	}
	muted, _ := json.Marshal(s.MutedMailAccounts)
	if err := st.SetStringSetting(a.ctx, "mutedMailAccounts", string(muted)); err != nil {
		return err
	}
	a.refreshTrayUnreadSoon()
	return nil
}

// settingOn 读一个布尔设置, 读失败时按默认值处理。
func (a *App) settingOn(key string, def bool) bool {
	st, err := a.currentStore()
	if err != nil {
		return def
	}
	v, err := st.BoolSetting(a.ctx, key, def)
	if err != nil {
		return def
	}
	return v
}
