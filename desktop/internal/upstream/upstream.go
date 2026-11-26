// Package upstream 读 czlmail 网关公布的上游邮箱状态。
//
// 网关把 IMAP / Gmail / Microsoft 邮箱统一成 JMAP: 一个网关登录下每个上游邮箱是一个账号,
// 账号级能力 `https://czl.net/jmap/upstream` 说明背后是哪种邮箱、现在能不能用。
// 上游不可用时依赖它的方法返回 serverUnavailable, 附 `czlUpstream: {status, code}`; 网关自己的凭据错才回 401。
// 客户端只读这两样, 不含任何服务商代码: 没有这个能力的账号 (普通 JMAP 服务器) 一切照旧。
package upstream

import (
	"encoding/json"
	"fmt"

	"git.sr.ht/~rockorager/go-jmap"
)

// URI 是账号级能力名。放在账号上而不是会话上: go-jmap 解析会话时丢掉不认识的会话级能力。
const URI jmap.URI = "https://czl.net/jmap/upstream"

// 上游状态。
const (
	StatusOK           = "ok"
	StatusSyncing      = "syncing"      // 首次导入中
	StatusAuthRequired = "authRequired" // 要到网关后台重新授权
	StatusUnreachable  = "unreachable"  // 连不上邮箱服务器, 或被服务商限流
	StatusPaused       = "paused"       // 用户或管理员暂停了
)

// Info 是账号的上游状态。
type Info struct {
	// Kind 是上游类型: imap / gmail / microsoft, 未来可能更多, 界面不认识的按通用邮箱显示。
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// StatusSince 是状态最近一次变化的时间 (RFC 3339, UTC), 原样转给界面。
	StatusSince string `json:"statusSince"`
	// Code 是非 ok 状态的网关错误码, ok 时为 0。
	Code int `json:"code"`
	// ReauthURL 是网关后台重新授权这个邮箱的页面, 只在 authRequired 时有。
	ReauthURL string `json:"reauthUrl"`
	// InitialSync 是首次导入的进度 (按邮件数), 导入完成后为空。
	InitialSync *Progress `json:"initialSync"`
}

// Progress 是首次导入的进度。
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// OK 报告上游是否正常。
func (i *Info) OK() bool { return i == nil || i.Status == "" || i.Status == StatusOK }

// Of 从账号的原始能力表读出上游状态, 不是网关账号 (没有这个能力) 时为 nil。
func Of(caps map[jmap.URI]json.RawMessage) *Info {
	raw, ok := caps[URI]
	if !ok {
		return nil
	}
	info := &Info{}
	if json.Unmarshal(raw, info) != nil {
		// 能力在但读不出: 仍是网关账号, 状态未知, 当作正常, 真不可用时方法错误会说明。
		return &Info{}
	}
	return info
}

// Decode 从缓存库存的能力表 (accounts.account_capabilities) 读出上游状态; 旧版存的 URI 数组没有能力对象, 读不出时为 nil。
func Decode(stored string) *Info {
	var caps map[jmap.URI]json.RawMessage
	if json.Unmarshal([]byte(stored), &caps) != nil {
		return nil
	}
	return Of(caps)
}

// Error 是网关因为上游不可用而拒绝一次调用的原因 (方法错误里的 czlUpstream)。
type Error struct {
	Status string `json:"status"`
	Code   int    `json:"code"`
}

// Error 以网关错误码开头, 界面按码显示本地化说明 (lib/api.ts 的 ERROR_MESSAGES)。
func (e *Error) Error() string {
	code := e.Code
	if code == 0 {
		code = defaultCode(e.Status)
	}
	switch code {
	case 40110:
		return "40110 the mailbox must be authorized again in the gateway admin"
	case 50210:
		return "50210 the gateway cannot reach the mailbox server"
	case 50310:
		return "50310 the gateway is still importing this mailbox"
	case 42910:
		return "42910 the mailbox provider is throttling the gateway"
	case 42310:
		return "42310 the mailbox is paused in the gateway"
	}
	return fmt.Sprintf("%d the gateway mailbox is unavailable (%s)", code, e.Status)
}

// defaultCode 是只给了状态没给码时按状态取的码。
func defaultCode(status string) int {
	switch status {
	case StatusAuthRequired:
		return 40110
	case StatusSyncing:
		return 50310
	case StatusPaused:
		return 42310
	}
	return 50210
}
