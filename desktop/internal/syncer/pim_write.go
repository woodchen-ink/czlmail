package syncer

import (
	"context"
	"encoding/json"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
)

// SetPIM 对日历、通讯录、文件对象执行一次 /set, 成功后立即增量同步该类型,
// 让界面不必等推送就能看到结果。返回新建对象的服务端 id(若有)。
//
// 与邮件一样不做本地乐观写入: 服务端会补全 uid、updated 等字段, 以它的结果为准。
func (s *Syncer) SetPIM(
	ctx context.Context, accountID, typeName string,
	create map[string]any, update map[string]map[string]any, destroy []string, extra map[string]any,
) (map[string]string, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Set(typeName, accountID, create, update, destroy, extra))
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	set, ok := resp.Responses[0].Args.(*jmapx.SetResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to " + typeName + "/set"}
	}
	if err := set.FirstError(); err != nil {
		return nil, &Error{Code: CodeMethod, Msg: typeName + "/set rejected", Err: err}
	}

	created := map[string]string{}
	for key, raw := range set.Created {
		var obj struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			created[key] = obj.ID
		}
	}

	s.ResyncPIM(ctx, accountID, typeName)
	return created, nil
}

// ResyncPIM 立即同步某账号的某个 PIM 类型并通知界面。
func (s *Syncer) ResyncPIM(ctx context.Context, accountID, typeName string) {
	for _, k := range pimKinds {
		if k.typeName != typeName {
			continue
		}
		s.mu.Lock()
		err := s.syncPIMKind(ctx, accountID, k)
		s.mu.Unlock()
		if err != nil {
			s.log.Warn("resync pim", "account", accountID, "type", typeName, "err", err)
			return
		}
		s.notifyPIM(accountID, typeName)
	}
}
