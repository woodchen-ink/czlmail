package mail

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

// maxMailboxName matches maxSizeMailboxName in the account capability.
const maxMailboxName = 490

func (s *Service) mailboxSet(ctx context.Context, call *jmapserver.Call) (any, error) {
	var args setArgs
	var extra struct {
		OnDestroyRemoveEmails bool `json:"onDestroyRemoveEmails"`
	}
	if err := call.Decode(&args); err != nil {
		return nil, err
	}
	if err := call.Decode(&extra); err != nil {
		return nil, err
	}
	if err := tooLarge(&args); err != nil {
		return nil, err
	}
	a, err := s.open(call, args.AccountID)
	if err != nil {
		return nil, err
	}
	state := a.state(mailindex.TypeMailbox)
	if err := checkState(args.IfInState, state); err != nil {
		return nil, err
	}
	resp := newSetResponse(a.id, state)
	if args.count() > 0 {
		if err := a.online(); err != nil {
			return nil, err
		}
	}
	ms := &mailboxSetter{s: s, call: call, a: a, resp: resp}
	// Parents are created before their children when the children refer to them.
	for _, cid := range createOrder(args.Create) {
		if err := ms.create(ctx, cid, args.Create[cid]); err != nil {
			return nil, err
		}
	}
	for id, patch := range args.Update {
		if err := ms.update(ctx, id, patch); err != nil {
			return nil, err
		}
	}
	for _, id := range dedupe(args.Destroy) {
		if err := ms.destroy(ctx, id, extra.OnDestroyRemoveEmails); err != nil {
			return nil, err
		}
	}
	resp.NewState = a.state(mailindex.TypeMailbox)
	return resp, nil
}

// createOrder sorts creation ids so a mailbox whose parentId is "#other" comes
// after other; cycles keep their input order and fail on the unresolved reference.
func createOrder(create map[string]map[string]json.RawMessage) []string {
	var out []string
	done := map[string]bool{}
	var visit func(cid string, depth int)
	visit = func(cid string, depth int) {
		if done[cid] || depth > len(create) {
			return
		}
		var parent string
		if raw, ok := create[cid]["parentId"]; ok && json.Unmarshal(raw, &parent) == nil && strings.HasPrefix(parent, "#") {
			if _, ok := create[parent[1:]]; ok {
				visit(parent[1:], depth+1)
			}
		}
		if !done[cid] {
			done[cid] = true
			out = append(out, cid)
		}
	}
	keys := make([]string, 0, len(create))
	for cid := range create {
		keys = append(keys, cid)
	}
	for _, cid := range keys {
		visit(cid, 0)
	}
	return out
}

type mailboxSetter struct {
	s    *Service
	call *jmapserver.Call
	a    *account
	resp *setResponse
}

func (ms *mailboxSetter) mailbox(id string) (*model.IndexMailbox, error) {
	var m model.IndexMailbox
	err := ms.a.idx.DB().Where("id = ?", id).Limit(1).Find(&m).Error
	if err != nil || m.ID == "" {
		return nil, err
	}
	return &m, nil
}

// parentOf decodes a parentId value ("" = top level), resolving "#creationId".
func (ms *mailboxSetter) parentOf(raw json.RawMessage) (string, *SetError) {
	if string(raw) == "null" {
		return "", nil
	}
	var p string
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", invalidProps("invalid value", "parentId")
	}
	real, ok := resolveID(ms.call, p)
	if !ok {
		return "", invalidProps("unknown creation id", "parentId")
	}
	m, err := ms.mailbox(real)
	if err != nil || m == nil {
		return "", invalidProps("no such mailbox", "parentId")
	}
	if m.Delimiter == "" {
		return "", invalidProps("the server has no folder hierarchy", "parentId")
	}
	return real, nil
}

func validName(raw json.RawMessage) (string, *SetError) {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil || name == "" {
		return "", invalidProps("name must be a non-empty string", "name")
	}
	if utf8.RuneCountInString(name) > maxMailboxName || strings.ContainsAny(name, "\r\n\x00") {
		return "", invalidProps("invalid name", "name")
	}
	return name, nil
}

var errMailboxRefused = invalidProps("the mail server refused the change", "name")

func (ms *mailboxSetter) create(ctx context.Context, cid string, props map[string]json.RawMessage) error {
	var name, parent string
	var se *SetError
	for k, raw := range props {
		switch k {
		case "name":
			name, se = validName(raw)
		case "parentId":
			parent, se = ms.parentOf(raw)
		case "role":
			if string(raw) != "null" {
				se = invalidProps("roles come from the mail server", "role")
			}
		case "isSubscribed", "sortOrder":
			// The server subscribes new folders and orders them itself; the
			// created response reports the values it chose.
		default:
			se = invalidProps("unknown or server-set property", k)
		}
		if se != nil {
			ms.resp.notCreated(cid, se)
			return nil
		}
	}
	if name == "" {
		ms.resp.notCreated(cid, invalidProps("name is required", "name"))
		return nil
	}
	id, _, err := ms.a.up.CreateMailbox(ctx, parent, name)
	if err != nil {
		se, ferr := ms.resp.upstreamFailure(err, errMailboxRefused)
		if ferr != nil {
			return ferr
		}
		ms.resp.notCreated(cid, se)
		return nil
	}
	ms.call.CreatedIDs[cid] = id
	ms.resp.wrote = true
	out := map[string]any{"id": id}
	if m, err := ms.mailbox(id); err != nil {
		ms.s.log.Warn("read created mailbox", "account", ms.a.id, "mailbox", id, "err", err)
	} else if m != nil {
		obj := mailboxObject(m, mailboxCounts{}, map[string]bool{"role": true, "sortOrder": true, "isSubscribed": true,
			"myRights": true, "totalEmails": true, "unreadEmails": true, "totalThreads": true, "unreadThreads": true})
		for k, v := range obj {
			out[k] = v
		}
	}
	ms.resp.created(cid, out)
	return nil
}

func (ms *mailboxSetter) update(ctx context.Context, rawID string, patch map[string]json.RawMessage) error {
	id, ok := resolveID(ms.call, rawID)
	if !ok {
		ms.resp.notUpdated(rawID, errNotFound)
		return nil
	}
	m, err := ms.mailbox(id)
	if err != nil {
		return err
	}
	if m == nil {
		ms.resp.notUpdated(rawID, errNotFound)
		return nil
	}
	name, parent := leafName(m), m.ParentID
	var se *SetError
	for k, raw := range patch {
		switch k {
		case "name":
			name, se = validName(raw)
		case "parentId":
			parent, se = ms.parentOf(raw)
			if se == nil && ms.isSelfOrDescendant(m, parent) {
				se = invalidProps("a mailbox cannot move under itself", "parentId")
			}
		case "role":
			var v *string
			if json.Unmarshal(raw, &v) != nil || (v == nil) != (m.Role == "") || v != nil && *v != m.Role {
				se = invalidProps("roles come from the mail server", "role")
			}
		case "isSubscribed":
			var v bool
			if json.Unmarshal(raw, &v) != nil || v != m.Subscribed {
				se = invalidProps("subscriptions cannot be changed through the gateway", "isSubscribed")
			}
		case "sortOrder":
			// Ordered by the server; ignored like on create.
		default:
			se = invalidProps("unknown or server-set property", k)
		}
		if se != nil {
			ms.resp.notUpdated(rawID, se)
			return nil
		}
	}
	if name != leafName(m) || parent != m.ParentID {
		if m.Role == "inbox" {
			ms.resp.notUpdated(rawID, errForbid)
			return nil
		}
		if _, err := ms.a.up.RenameMailbox(ctx, id, parent, name); err != nil {
			se, ferr := ms.resp.upstreamFailure(err, errMailboxRefused)
			if ferr != nil {
				return ferr
			}
			ms.resp.notUpdated(rawID, se)
			return nil
		}
	}
	ms.resp.updated(rawID, nil)
	return nil
}

func (ms *mailboxSetter) isSelfOrDescendant(m *model.IndexMailbox, parent string) bool {
	for p, depth := parent, 0; p != "" && depth < 1000; depth++ {
		if p == m.ID {
			return true
		}
		pm, err := ms.mailbox(p)
		if err != nil || pm == nil {
			return false
		}
		p = pm.ParentID
	}
	return false
}

func (ms *mailboxSetter) destroy(ctx context.Context, rawID string, removeEmails bool) error {
	id, ok := resolveID(ms.call, rawID)
	if !ok {
		ms.resp.notDestroyed(rawID, errNotFound)
		return nil
	}
	m, err := ms.mailbox(id)
	if err != nil {
		return err
	}
	if m == nil {
		ms.resp.notDestroyed(rawID, errNotFound)
		return nil
	}
	if m.Role == "inbox" {
		ms.resp.notDestroyed(rawID, errForbid)
		return nil
	}
	var children int64
	if err := ms.a.idx.DB().Model(&model.IndexMailbox{}).Where("parent_id = ?", id).Count(&children).Error; err != nil {
		return err
	}
	if children > 0 {
		ms.resp.notDestroyed(rawID, setErr("mailboxHasChild", ""))
		return nil
	}
	if !removeEmails {
		var n int64
		if err := ms.a.idx.DB().Model(&model.IndexMembership{}).Where("mailbox_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			ms.resp.notDestroyed(rawID, setErr("mailboxHasEmail", ""))
			return nil
		}
	}
	// IMAP DELETE removes the folder's messages with it.
	if _, err := ms.a.up.DeleteMailbox(ctx, id); err != nil {
		se, ferr := ms.resp.upstreamFailure(err, setErr("forbidden", "the mail server refused to delete the mailbox"))
		if ferr != nil {
			return ferr
		}
		ms.resp.notDestroyed(rawID, se)
		return nil
	}
	ms.resp.destroyed(rawID)
	return nil
}
