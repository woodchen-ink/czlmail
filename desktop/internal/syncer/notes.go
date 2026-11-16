package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"git.sr.ht/~rockorager/go-jmap"
	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/notes"
)

// FileSnapshot reads a consistent complete FileNode view without trusting stale cache metadata.
func (s *Syncer) FileSnapshot(ctx context.Context, account string) (notes.Snapshot, error) {
	if !s.accountSupports(account, jmapx.FileNodeURI) {
		return notes.Snapshot{}, notes.ErrUnavailable
	}
	state, err := s.fileState(ctx, account)
	if err != nil {
		return notes.Snapshot{}, err
	}
	ids := []string{}
	queryState := ""
	for {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&jmapx.Call{Method: "FileNode/query", Args: map[string]any{"accountId": account, "position": len(ids), "limit": 500, "calculateTotal": true}})
		resp, err := s.do(req)
		if err != nil {
			return notes.Snapshot{}, notes.ErrUnavailable
		}
		if len(resp.Responses) != 1 {
			return notes.Snapshot{}, notes.ErrUnavailable
		}
		q, ok := resp.Responses[0].Args.(*jmapx.QueryResponse)
		if !ok {
			return notes.Snapshot{}, notes.ErrUnavailable
		}
		if queryState != "" && q.QueryState != queryState {
			return notes.Snapshot{}, notes.ErrStateChanged
		}
		queryState = q.QueryState
		ids = append(ids, q.IDs...)
		if len(q.IDs) == 0 || q.Total > 0 && len(ids) >= q.Total {
			break
		}
		if len(ids) > 100000 {
			return notes.Snapshot{}, notes.ErrInvalid
		}
	}
	raw, err := s.getPIM(ctx, account, jmapx.FileNode, ids)
	if err != nil {
		return notes.Snapshot{}, notes.ErrUnavailable
	}
	result := notes.Snapshot{State: state, Nodes: []notes.Node{}}
	for _, data := range raw {
		n, err := FileNodeFrom(data)
		if err != nil {
			return notes.Snapshot{}, notes.ErrUnavailable
		}
		result.Nodes = append(result.Nodes, notes.Node{ID: n.ID, ParentID: n.ParentID, Name: n.Name, Directory: n.IsDirectory, BlobID: n.BlobID, Type: n.ContentType, Size: n.Size, Created: n.Created * 1000, Modified: n.Modified * 1000})
	}
	after, err := s.fileState(ctx, account)
	if err != nil {
		return notes.Snapshot{}, err
	}
	if after != state {
		return notes.Snapshot{}, notes.ErrStateChanged
	}
	return result, nil
}

// FileChanges 取 since 之后变过的 FileNode: 每分钟的对账不必重新列出整个网盘。
// state 被服务器回收 (cannotCalculateChanges) 等任何失败都原样返回, 调用方退回 FileSnapshot。
func (s *Syncer) FileChanges(ctx context.Context, account, since string) (notes.Delta, error) {
	out := notes.Delta{State: since}
	touched := map[string]bool{}
	for {
		req := &jmap.Request{Context: ctx}
		req.Invoke(jmapx.Changes(jmapx.FileNode, account, out.State, s.maxObjectsInGet))
		resp, err := s.do(req)
		if err != nil || len(resp.Responses) != 1 {
			return notes.Delta{}, notes.ErrUnavailable
		}
		ch, ok := resp.Responses[0].Args.(*jmapx.ChangesResponse)
		if !ok || ch.NewState == "" {
			return notes.Delta{}, notes.ErrUnavailable
		}
		for _, id := range ch.Created {
			touched[id] = true
		}
		for _, id := range ch.Updated {
			touched[id] = true
		}
		for _, id := range ch.Destroyed {
			delete(touched, id)
			out.Destroyed = append(out.Destroyed, id)
		}
		out.State = ch.NewState
		if !ch.HasMoreChanges {
			break
		}
	}
	if len(touched) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	raw, err := s.getPIM(ctx, account, jmapx.FileNode, ids)
	if err != nil {
		return notes.Delta{}, notes.ErrUnavailable
	}
	seen := map[string]bool{}
	for _, data := range raw {
		n, err := FileNodeFrom(data)
		if err != nil {
			return notes.Delta{}, notes.ErrUnavailable
		}
		seen[n.ID] = true
		out.Changed = append(out.Changed, notes.Node{ID: n.ID, ParentID: n.ParentID, Name: n.Name, Directory: n.IsDirectory, BlobID: n.BlobID, Type: n.ContentType, Size: n.Size, Created: n.Created * 1000, Modified: n.Modified * 1000})
	}
	// 变更里报了却取不到: 可能刚被删, 也可能是服务器少返回了。分不清就不猜, 让调用方退回全量快照,
	// 否则一篇已同步的笔记会被当成「远端已删除」。
	for _, id := range ids {
		if !seen[id] {
			return notes.Delta{}, notes.ErrUnavailable
		}
	}
	return out, nil
}

// fileState fetches the account-level state even when there are no objects.
func (s *Syncer) fileState(ctx context.Context, account string) (string, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Get(jmapx.FileNode, account, []string{}))
	resp, err := s.do(req)
	if err != nil || len(resp.Responses) != 1 {
		return "", notes.ErrUnavailable
	}
	get, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok || get.State == "" {
		return "", notes.ErrUnavailable
	}
	return get.State, nil
}

// verifyFileConditions probes with a well-formed unequal state and no mutations.
// Stalwart may accept a valid encoded state's suffix; concatenation is not an unequal-state test.
// Unsupported state formats fail closed before any actual note update.
func (s *Syncer) verifyFileConditions(ctx context.Context, account string) error {
	if _, ok := s.fileCAS.Load(account); ok {
		return nil
	}
	state, err := s.fileState(ctx, account)
	if err != nil {
		return err
	}
	req := &jmap.Request{Context: ctx}
	probeState := "n"
	if state == probeState {
		probeState = "sae"
	}
	req.Invoke(jmapx.Set(jmapx.FileNode, account, nil, nil, nil, map[string]any{"ifInState": probeState}))
	_, err = s.do(req)
	if methodErrorType(err) != "stateMismatch" {
		return notes.ErrUnavailable
	}
	s.fileCAS.Store(account, true)
	return nil
}

// InspectFile reads one target and the associated current state for a guarded write.
func (s *Syncer) InspectFile(ctx context.Context, account, id string) (notes.Snapshot, error) {
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	}
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Get(jmapx.FileNode, account, ids))
	resp, err := s.do(req)
	if err != nil || len(resp.Responses) != 1 {
		return notes.Snapshot{}, notes.ErrUnavailable
	}
	got, ok := resp.Responses[0].Args.(*jmapx.GetResponse)
	if !ok || got.State == "" {
		return notes.Snapshot{}, notes.ErrUnavailable
	}
	out := notes.Snapshot{State: got.State, Nodes: []notes.Node{}}
	for _, raw := range got.List {
		n, err := FileNodeFrom(raw)
		if err != nil {
			return notes.Snapshot{}, notes.ErrUnavailable
		}
		out.Nodes = append(out.Nodes, notes.Node{ID: n.ID, ParentID: n.ParentID, Name: n.Name, BlobID: n.BlobID, Directory: n.IsDirectory, Type: n.ContentType, Size: n.Size})
	}
	return out, nil
}

// setFileOnce 发一次带 ifInState 的 FileNode/set, 对象按 typed 指定的写法改写。
func (s *Syncer) setFileOnce(ctx context.Context, account, state string, typed bool, create map[string]any, update map[string]map[string]any) (*jmap.Response, error) {
	c, u := adaptFileNodeWrite(typed, create, update)
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Set(jmapx.FileNode, account, c, u, nil, map[string]any{"ifInState": state}))
	return s.do(req)
}

// SetFileConditional never silently falls back when ifInState is unsupported or rejected.
func (s *Syncer) SetFileConditional(ctx context.Context, account, state, id string, n notes.Node) (notes.Node, error) {
	if err := s.verifyFileConditions(ctx, account); err != nil {
		return notes.Node{}, err
	}
	if state == "" {
		return notes.Node{}, notes.ErrInvalid
	}
	obj := map[string]any{"name": n.Name, "parentId": nil}
	if n.ParentID != "" {
		obj["parentId"] = n.ParentID
	}
	// 已有文件不带 blobId: 只改名 / 移动, 正文沿用服务器上那一份。
	if !n.Directory && (id == "" || n.BlobID != "") {
		obj["blobId"] = n.BlobID
		obj["type"] = n.Type
	}
	var create map[string]any
	var update map[string]map[string]any
	if id == "" {
		create = map[string]any{"note": obj}
	} else {
		update = map[string]map[string]any{id: obj}
	}
	typed := s.fileSchemaTyped(ctx, account)
	resp, err := s.setFileOnce(ctx, account, state, typed, create, update)
	if err == nil && len(resp.Responses) == 1 {
		// 写法猜错被拒时换另一种重试一次; 被拒的写入不改变 state, ifInState 仍然成立。
		if set, ok := resp.Responses[0].Args.(*jmapx.SetResponse); ok && rejectsFileSchema(set.FirstError()) {
			if retry, retryErr := s.setFileOnce(ctx, account, state, !typed, create, update); retryErr == nil {
				resp, err = retry, nil
				if len(retry.Responses) == 1 {
					if rs, ok := retry.Responses[0].Args.(*jmapx.SetResponse); ok && rs.FirstError() == nil {
						s.fileSchema.Store(account, !typed)
					}
				}
			}
		}
	}
	if err != nil {
		// stateMismatch 是账号级的: 网盘里别的文件变了也会撞上, 不是这篇笔记的冲突。
		kind := methodErrorType(err)
		if kind == "stateMismatch" {
			return notes.Node{}, notes.ErrStateChanged
		}
		var method *Error
		if errors.As(err, &method) && method.Code == CodeMethod {
			return notes.Node{}, fmt.Errorf("%w: %s", noteWriteError(kind), kind)
		}
		return notes.Node{}, notes.ErrUncertain
	}
	if len(resp.Responses) != 1 {
		return notes.Node{}, notes.ErrUncertain
	}
	set, ok := resp.Responses[0].Args.(*jmapx.SetResponse)
	if !ok {
		return notes.Node{}, notes.ErrUncertain
	}
	if setErr := set.FirstError(); setErr != nil {
		// 带上服务器给的原因 (类型与描述), 界面的「详情」与日志靠它排查。
		kind := ""
		var se *jmapx.SetError
		if errors.As(setErr, &se) {
			kind = se.Type
		}
		return notes.Node{}, fmt.Errorf("%w: %v", noteWriteError(kind), setErr)
	}
	if id == "" {
		var created struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(set.Created["note"], &created) != nil || created.ID == "" {
			return notes.Node{}, notes.ErrUncertain
		}
		n.ID = created.ID
	} else {
		n.ID = id
	}
	// File metadata changes are also made visible to the ordinary Files module.
	s.ResyncPIM(ctx, account, jmapx.FileNode)
	// Return the persistent server blob identifier, not the temporary upload identifier.
	if latest, readErr := s.InspectFile(ctx, account, n.ID); readErr == nil && latest.State == set.NewState && len(latest.Nodes) == 1 && latest.Nodes[0].ID == n.ID {
		n = latest.Nodes[0]
	}
	return n, nil
}

// noteWriteError 把服务器拒绝写入的类型归到笔记的错误码: 权限、配额、大小各自一类,
// 其余 (invalidProperties 等) 是「服务器不接受这次写入」。
func noteWriteError(kind string) error {
	switch kind {
	case "serverFail", "serverUnavailable", "rateLimit":
		// 服务器一时的问题, 退避后自动重试。
		return notes.ErrUnavailable
	case "serverPartialFail":
		// 可能写进去了一部分: 按结果不明处理, 下一轮核对。
		return notes.ErrUncertain
	case "alreadyExists":
		// 同名文件在读与写之间出现了: 刷新视图后由拉取配对, 不是这条记录的错。
		return notes.ErrStateChanged
	case "forbidden", "accountReadOnly":
		return notes.ErrForbidden
	case "overQuota":
		return notes.ErrQuota
	case "tooLarge", "requestTooLarge":
		return notes.ErrTooLarge
	}
	return notes.ErrRejected
}
