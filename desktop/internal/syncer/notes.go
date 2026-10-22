package syncer

import (
	"context"
	"encoding/json"
	"errors"

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
			return notes.Snapshot{}, notes.ErrConflict
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
		return notes.Snapshot{}, notes.ErrConflict
	}
	return result, nil
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
	if !n.Directory {
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
	req := &jmap.Request{Context: ctx}
	req.Invoke(jmapx.Set(jmapx.FileNode, account, create, update, nil, map[string]any{"ifInState": state}))
	resp, err := s.do(req)
	if err != nil {
		if methodErrorType(err) == "stateMismatch" {
			return notes.Node{}, notes.ErrConflict
		}
		var method *Error
		if errors.As(err, &method) && method.Code == CodeMethod {
			return notes.Node{}, notes.ErrForbidden
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
	if set.FirstError() != nil {
		return notes.Node{}, notes.ErrForbidden
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
