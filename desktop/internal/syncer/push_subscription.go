package syncer

import (
	"context"
	"encoding/json"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// 推送订阅 (RFC 8620 §7.2) 与新邮件推送扩展 (draft-ietf-jmap-emailpush)。
// 只有移动端用: 程序被系统挂起后收不到 EventSource, 改由服务端把新邮件推到推送中转, 再转成系统通知。

// PushSubscription 是服务端返回的订阅。keys 与 url 服务端不回显 (keys 恒为 null)。
type PushSubscription struct {
	ID             string          `json:"id"`
	DeviceClientID string          `json:"deviceClientId"`
	URL            string          `json:"url"`
	Expires        string          `json:"expires"`
	Types          []string        `json:"types"`
	EmailPush      json.RawMessage `json:"emailPush"`
}

// EmailPushConfig 是某个账号的推送条件: filter 命中的新邮件推送 properties 列出的属性 (按重要程度排, 放不下时从后往前丢)。
type EmailPushConfig struct {
	Filter     any      `json:"filter"`
	Properties []string `json:"properties"`
	Urgency    string   `json:"urgency,omitempty"`
}

// SupportsEmailPush 报告账号是否公布了新邮件推送扩展。
// 只看账号级能力: go-jmap 解析会话时丢掉它不认识的会话级能力, Session.Capabilities 里永远查不到 emailpush。
func (s *Syncer) SupportsEmailPush(accountID string) bool {
	if s.client.Session == nil {
		return false
	}
	acc, ok := s.client.Session.Accounts[jmap.ID(accountID)]
	if !ok {
		return false
	}
	_, ok = acc.RawCapabilities[jmapx.EmailPushURI]
	return ok
}

// PushSubscriptions 列出本次认证可见的全部推送订阅 (同一用户的其他设备也在内)。
func (s *Syncer) PushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&jmapx.Call{Method: jmapx.PushSubscription + "/get", Args: map[string]any{"ids": nil}})
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	get, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to PushSubscription/get"}
	}
	out := make([]PushSubscription, 0, len(get.List))
	for _, raw := range get.List {
		var sub PushSubscription
		if err := json.Unmarshal(raw, &sub); err != nil {
			return nil, wrap(CodeUnhandled, "decode push subscription", err)
		}
		out = append(out, sub)
	}
	return out, nil
}

// SetPushSubscription 执行一次 PushSubscription/set, 返回新建订阅的 id (未新建时为空)。
func (s *Syncer) SetPushSubscription(ctx context.Context, create map[string]any, update map[string]map[string]any, destroy []string) (string, error) {
	args := map[string]any{}
	if create != nil {
		args["create"] = map[string]any{"s": create}
	}
	if len(update) > 0 {
		args["update"] = update
	}
	if len(destroy) > 0 {
		args["destroy"] = destroy
	}
	req := &jmap.Request{Context: ctx}
	req.Invoke(&jmapx.Call{Method: jmapx.PushSubscription + "/set", Args: args})
	resp, err := s.do(req)
	if err != nil {
		return "", err
	}
	set, ok := resp.Responses[0].Args.(*jmapx.SetResponse)
	if !ok {
		return "", &Error{Code: CodeUnhandled, Msg: "unexpected response to PushSubscription/set"}
	}
	if err := set.FirstError(); err != nil {
		return "", &Error{Code: CodeMethod, Msg: "PushSubscription/set rejected", Err: err}
	}
	var created struct {
		ID string `json:"id"`
	}
	if raw, ok := set.Created["s"]; ok {
		_ = json.Unmarshal(raw, &created)
	}
	return created.ID, nil
}
