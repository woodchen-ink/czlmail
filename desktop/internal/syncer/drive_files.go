package syncer

import (
	"context"
	"encoding/json"
	"errors"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 网盘里的同步文件 (模板、各类设置、附加登录) 按"读到哪个版本, 就只在网盘还是那个版本时写"更新:
// 先让本地的网盘缓存追上服务器 (CatchUpFiles), 按缓存里的内容合并, 再带 ifInState 写回 (SetFilesIfInState)。
// 别的设备在这之间写过网盘, 服务器拒绝这次写入, 调用方重新追上、重新合并, 不会盖掉对方刚写的内容。

// ErrFilesChanged 表示读与写之间网盘变了 (ifInState 不符, 或要新建的文件已被别人建出来), 重读后重试。
// ifInState 是整个网盘的版本号, 别的文件变了也会撞上。
var ErrFilesChanged = errors.New("2176 the personal files changed while syncing; retrying")

// CatchUpFiles 把某账号的网盘缓存增量同步到服务器当前版本, 返回缓存对应的 state。
// 只在缓存真的变了时通知界面: 网盘变化会触发各同步文件的合并, 合并又先调这里, 无条件通知会来回触发。
func (s *Syncer) CatchUpFiles(ctx context.Context, accountID string) (string, error) {
	if err := s.Require(accountID, jmapx.FileNodeURI); err != nil {
		return "", err
	}
	var kind pimKind
	for _, k := range pimKinds {
		if k.typeName == jmapx.FileNode {
			kind = k
		}
	}
	s.mu.Lock()
	before, _ := s.store.State(ctx, accountID, store.TypeFileNode)
	err := s.syncPIMKind(ctx, accountID, kind)
	after, stateErr := s.store.State(ctx, accountID, store.TypeFileNode)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if stateErr != nil {
		return "", stateErr
	}
	if after != before {
		s.notifyPIM(accountID, jmapx.FileNode)
	}
	return after, nil
}

// SetFilesIfInState 带 ifInState 执行一次 FileNode/set, 返回新建对象的 id 与写入后的 state。
// 服务器确定不支持 ifInState 时退回不带条件的写入: 设置类文件逐条合并, 偶尔被覆盖下一轮会补回来, 不值得为此停掉同步。
// 写成功后让缓存追上, 调用方之后读缓存能看到这次写入。
func (s *Syncer) SetFilesIfInState(ctx context.Context, accountID, state string, create map[string]any, update map[string]map[string]any) (map[string]string, string, error) {
	if err := s.Require(accountID, jmapx.FileNodeURI); err != nil {
		return nil, "", err
	}
	extra := map[string]any{}
	if state != "" {
		ok, err := s.supportsIfInState(ctx, accountID)
		if err != nil {
			return nil, "", err
		}
		if ok {
			extra["ifInState"] = state
		}
	}
	send := func(typed bool) (*jmapx.SetResponse, error) {
		c, u := adaptFileNodeWrite(typed, create, update)
		req := &jmap.Request{Context: ctx}
		req.Invoke(jmapx.Set(jmapx.FileNode, s.sid(accountID), c, u, nil, extra))
		resp, err := s.do(req)
		if err != nil {
			return nil, err
		}
		set, ok := resp.Responses[0].Args.(*jmapx.SetResponse)
		if !ok {
			return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to FileNode/set"}
		}
		return set, nil
	}
	typed := s.fileSchemaTyped(ctx, accountID)
	set, err := send(typed)
	if err == nil && rejectsFileSchema(set.FirstError()) {
		// 写法猜错被拒的写入不改变 state, ifInState 仍然成立。
		retry, retryErr := send(!typed)
		switch {
		case retryErr == nil && retry.FirstError() == nil:
			s.fileSchema.Store(accountID, !typed)
			set = retry
		case methodErrorType(retryErr) == "stateMismatch":
			return nil, "", ErrFilesChanged
		}
	}
	if err != nil {
		if methodErrorType(err) == "stateMismatch" {
			return nil, "", ErrFilesChanged
		}
		return nil, "", err
	}
	if setErr := set.FirstError(); setErr != nil {
		var se *jmapx.SetError
		if errors.As(setErr, &se) && se.Type == "alreadyExists" {
			return nil, "", ErrFilesChanged
		}
		return nil, "", &Error{Code: CodeMethod, Msg: "FileNode/set rejected", Err: setErr}
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
	if _, err := s.CatchUpFiles(ctx, accountID); err != nil {
		s.log.Warn("catch up files after write", "account", accountID, "err", err)
	}
	return created, set.NewState, nil
}

// supportsIfInState 报告服务器是否按 ifInState 拒绝过时的写入。用一个不可能的 state 发一次空的 FileNode/set 探测:
// 回 stateMismatch 即支持; 照常成功说明服务器忽略了它 (确定不支持, 记下来以后不再探测);
// 断网、服务器出错不下结论, 返回错误, 这一轮不写 (不能因为一次探测失败就退回不带条件的写入)。
func (s *Syncer) supportsIfInState(ctx context.Context, accountID string) (bool, error) {
	if _, ok := s.fileCAS.Load(accountID); ok {
		return true, nil
	}
	if _, ok := s.fileNoCAS.Load(accountID); ok {
		return false, nil
	}
	state, err := s.fileState(ctx, accountID)
	if err != nil {
		return false, err
	}
	probe := "n"
	if state == probe {
		probe = "sae"
	}
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Set(jmapx.FileNode, s.sid(accountID), nil, nil, nil, map[string]any{"ifInState": probe}))
	_, err = s.do(req)
	switch {
	case methodErrorType(err) == "stateMismatch":
		s.fileCAS.Store(accountID, true)
		return true, nil
	case err == nil:
		s.log.Warn("server ignores ifInState; drive sync files are written unconditionally", "account", accountID)
		s.fileNoCAS.Store(accountID, true)
		return false, nil
	}
	return false, err
}
