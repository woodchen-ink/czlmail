package jmaptest

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type request struct {
	Using       []string          `json:"using"`
	MethodCalls []json.RawMessage `json:"methodCalls"`
}

// invocation 是一个方法调用或响应: [name, args, callId]。
type invocation struct {
	name   string
	args   map[string]any
	callID string
}

func (inv invocation) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{inv.name, inv.args, inv.callID})
}

// methodError 是 JMAP 方法级错误。
type methodError string

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, u *User) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ex := &exchange{s: s, u: u, created: map[string]string{}}
	for _, raw := range req.MethodCalls {
		var triple []json.RawMessage
		if err := json.Unmarshal(raw, &triple); err != nil || len(triple) != 3 {
			http.Error(w, "bad invocation", http.StatusBadRequest)
			return
		}
		var inv invocation
		_ = json.Unmarshal(triple[0], &inv.name)
		_ = json.Unmarshal(triple[1], &inv.args)
		_ = json.Unmarshal(triple[2], &inv.callID)
		if inv.args == nil {
			inv.args = map[string]any{}
		}
		ex.run(inv, triple[1])
	}
	s.mu.Lock()
	state := strconv.FormatInt(s.sessionGen, 10)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"methodResponses": ex.responses, "sessionState": state})
}

// exchange 是一次 API 请求的处理过程: 已有的响应 (供 back-reference) 与本请求里新建对象的 id (供 "#创建 id" 引用)。
type exchange struct {
	s         *Server
	u         *User
	responses []invocation
	created   map[string]string
	touched   []*Account
}

func (ex *exchange) run(inv invocation, rawArgs json.RawMessage) {
	if err := ex.resolveRefs(inv.args); err != "" {
		ex.fail(inv, err)
		return
	}
	accID, _ := inv.args["accountId"].(string)
	call := Call{User: ex.u.name, Method: inv.name, AccountID: accID, Args: slices.Clone(rawArgs)}
	s := ex.s
	s.mu.Lock()
	s.calls = append(s.calls, call)
	hook := s.onMethod
	s.mu.Unlock()
	if hook != nil {
		hook(call)
	}

	s.mu.Lock()
	acc := s.accountForLocked(ex.u, accID)
	if acc == nil && !strings.HasPrefix(inv.name, "Principal/") {
		s.mu.Unlock()
		ex.fail(inv, "accountNotFound")
		return
	}
	typ, method, _ := strings.Cut(inv.name, "/")
	if acc != nil && acc.unavailable != nil && typ != "PushSubscription" {
		up := acc.unavailable
		s.mu.Unlock()
		ex.responses = append(ex.responses, invocation{name: "error",
			args: map[string]any{"type": "serverUnavailable", "czlUpstream": up}, callID: inv.callID})
		return
	}
	if acc != nil && !acc.supportsLocked(typ) {
		s.mu.Unlock()
		ex.fail(inv, "unknownMethod")
		return
	}
	var extra []invocation
	res, merr := ex.dispatchLocked(acc, typ, method, inv.args, &extra)
	s.mu.Unlock()
	if merr != "" {
		ex.fail(inv, merr)
		return
	}
	ex.responses = append(ex.responses, invocation{name: inv.name, args: res, callID: inv.callID})
	for _, e := range extra {
		e.callID = inv.callID
		ex.responses = append(ex.responses, e)
	}
	for _, a := range ex.touched {
		s.changed(a)
	}
	ex.touched = nil
}

func (ex *exchange) fail(inv invocation, typ methodError) {
	ex.responses = append(ex.responses, invocation{name: "error", args: map[string]any{"type": string(typ)}, callID: inv.callID})
}

// supportsLocked 报告账号是否声明了某类型所属的能力。
func (a *Account) supportsLocked(typ string) bool {
	need := ""
	switch typ {
	case "Mailbox", "Email", "Thread", "SearchSnippet", "Identity":
		need = Mail
	case "EmailSubmission":
		need = Submission
	case "VacationResponse":
		need = VacationResponse
	case "Calendar", "CalendarEvent", "ParticipantIdentity", "CalendarEventNotification":
		need = Calendars
	case "AddressBook", "ContactCard":
		need = Contacts
	case "FileNode":
		need = FileNode
	case "SieveScript":
		need = Sieve
	case "PushSubscription", "Principal":
		return true
	default:
		return false
	}
	if typ == "Identity" {
		return slices.Contains(a.caps, Mail) || slices.Contains(a.caps, Submission)
	}
	return slices.Contains(a.caps, need)
}

// resolveRefs 把 "#属性": ResultReference 换成前面响应里取出的值 (RFC 8620 3.7)。
func (ex *exchange) resolveRefs(args map[string]any) methodError {
	for k, v := range args {
		name, ok := strings.CutPrefix(k, "#")
		if !ok {
			continue
		}
		ref, _ := v.(map[string]any)
		resultOf, _ := ref["resultOf"].(string)
		refName, _ := ref["name"].(string)
		path, _ := ref["path"].(string)
		var found *invocation
		for i := range ex.responses {
			if ex.responses[i].callID == resultOf && ex.responses[i].name == refName {
				found = &ex.responses[i]
				break
			}
		}
		if found == nil {
			return "invalidResultReference"
		}
		val, ok := pointer(found.args, strings.Split(strings.TrimPrefix(path, "/"), "/"))
		if !ok {
			return "invalidResultReference"
		}
		delete(args, k)
		args[name] = val
	}
	return ""
}

// pointer 取 JSON Pointer 指向的值, "*" 展开数组。
func pointer(v any, path []string) (any, bool) {
	if len(path) == 0 || (len(path) == 1 && path[0] == "") {
		return v, true
	}
	head, rest := path[0], path[1:]
	switch t := v.(type) {
	case map[string]any:
		next, ok := t[head]
		if !ok {
			return nil, false
		}
		return pointer(next, rest)
	case []any:
		if head != "*" {
			i, err := strconv.Atoi(head)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			return pointer(t[i], rest)
		}
		out := []any{}
		for _, item := range t {
			got, ok := pointer(item, rest)
			if !ok {
				return nil, false
			}
			if arr, isArr := got.([]any); isArr {
				out = append(out, arr...)
			} else {
				out = append(out, got)
			}
		}
		return out, true
	case []string:
		anys := make([]any, len(t))
		for i, s := range t {
			anys[i] = s
		}
		return pointer(anys, path)
	}
	return nil, false
}

func (ex *exchange) dispatchLocked(acc *Account, typ, method string, args map[string]any, extra *[]invocation) (map[string]any, methodError) {
	switch {
	case typ == "Principal" && method == "get":
		return map[string]any{"accountId": args["accountId"], "state": "0", "list": []any{}, "notFound": []any{}}, ""
	case typ == "Email" && method == "query":
		return acc.queryEmailsLocked(args), ""
	case typ == "Email" && method == "set":
		return ex.setLocked(acc, typ, args)
	case typ == "Email" && method == "import":
		return ex.importEmailsLocked(acc, args), ""
	case typ == "EmailSubmission" && method == "set":
		return ex.submitLocked(acc, args, extra)
	case typ == "EmailSubmission" && method == "query":
		return map[string]any{"accountId": acc.id, "queryState": acc.stateLocked(typ), "ids": []any{}, "position": 0, "total": 0, "canCalculateChanges": false}, ""
	case typ == "VacationResponse" && acc.getLocked(typ, "singleton") == nil:
		acc.putLocked(typ, "singleton", map[string]any{"isEnabled": false, "fromDate": nil, "toDate": nil, "subject": nil, "textBody": nil, "htmlBody": nil})
		return ex.dispatchLocked(acc, typ, method, args, extra)
	case typ == "SieveScript" && method == "validate":
		return map[string]any{"accountId": acc.id, "error": nil}, ""
	case method == "get":
		return acc.getObjectsLocked(typ, args), ""
	case method == "changes":
		return acc.changesLocked(typ, args)
	case method == "query":
		return acc.queryObjectsLocked(typ, args), ""
	case method == "set":
		return ex.setLocked(acc, typ, args)
	}
	return nil, "unknownMethod"
}

func (a *Account) getObjectsLocked(typ string, args map[string]any) map[string]any {
	c := a.coll(typ)
	list := []any{}
	notFound := []any{}
	view := func(m map[string]any) map[string]any {
		if typ == "Mailbox" {
			return a.mailboxViewLocked(m)
		}
		return m
	}
	if ids, ok := args["ids"].([]any); ok {
		for _, raw := range ids {
			id, _ := raw.(string)
			if o, ok := c.objs[id]; ok {
				list = append(list, view(o.data))
			} else {
				notFound = append(notFound, id)
			}
		}
	} else {
		keys := make([]string, 0, len(c.objs))
		for id := range c.objs {
			keys = append(keys, id)
		}
		slices.Sort(keys)
		for _, id := range keys {
			list = append(list, view(c.objs[id].data))
		}
	}
	return map[string]any{"accountId": a.id, "state": a.stateLocked(typ), "list": list, "notFound": notFound}
}

func (a *Account) changesLocked(typ string, args map[string]any) (map[string]any, methodError) {
	since, err := strconv.ParseInt(stringArg(args, "sinceState"), 10, 64)
	if err != nil || since > a.stamp || since < 0 {
		return nil, "cannotCalculateChanges"
	}
	c := a.coll(typ)
	created, updated, destroyed := []any{}, []any{}, []any{}
	for id, o := range c.objs {
		switch {
		case o.created > since:
			created = append(created, id)
		case o.updated > since:
			updated = append(updated, id)
		}
	}
	for id, g := range c.gone {
		if g[0] <= since && g[1] > since {
			destroyed = append(destroyed, id)
		}
	}
	res := map[string]any{
		"accountId": a.id, "oldState": stringArg(args, "sinceState"), "newState": a.stateLocked(typ),
		"hasMoreChanges": false, "created": created, "updated": updated, "destroyed": destroyed,
	}
	if typ == "Mailbox" {
		res["updatedProperties"] = nil
	}
	return res, ""
}

// queryObjectsLocked 是通用的 /query: 过滤条件里的标量属性按相等比较, 其余条件忽略; 按 id 排序。
func (a *Account) queryObjectsLocked(typ string, args map[string]any) map[string]any {
	filter, _ := args["filter"].(map[string]any)
	ids := []any{}
	keys := make([]string, 0)
	for id, o := range a.coll(typ).objs {
		if matchScalars(o.data, filter) {
			keys = append(keys, id)
		}
	}
	slices.Sort(keys)
	for _, id := range keys {
		ids = append(ids, id)
	}
	return map[string]any{
		"accountId": a.id, "queryState": a.stateLocked(typ), "canCalculateChanges": false,
		"position": 0, "ids": ids, "total": len(ids),
	}
}

func matchScalars(obj, filter map[string]any) bool {
	for k, want := range filter {
		switch want.(type) {
		case string, bool, float64, nil:
			if obj[k] != want {
				return false
			}
		}
	}
	return true
}

func (a *Account) queryEmailsLocked(args map[string]any) map[string]any {
	filter, _ := args["filter"].(map[string]any)
	type hit struct {
		id string
		at string
	}
	var hits []hit
	for id, o := range a.coll("Email").objs {
		if emailMatches(o.data, filter) {
			at, _ := o.data["receivedAt"].(string)
			hits = append(hits, hit{id, at})
		}
	}
	asc := false
	if sort, ok := args["sort"].([]any); ok && len(sort) > 0 {
		if c, ok := sort[0].(map[string]any); ok {
			asc, _ = c["isAscending"].(bool)
		}
	}
	slices.SortFunc(hits, func(x, y hit) int {
		if c := strings.Compare(x.at, y.at); c != 0 {
			if asc {
				return c
			}
			return -c
		}
		return strings.Compare(x.id, y.id)
	})
	pos := int(numArg(args, "position"))
	if pos < 0 {
		pos = max(len(hits)+pos, 0)
	}
	pos = min(pos, len(hits))
	end := len(hits)
	if limit := int(numArg(args, "limit")); limit > 0 {
		end = min(pos+limit, len(hits))
	}
	ids := []any{}
	for _, h := range hits[pos:end] {
		ids = append(ids, h.id)
	}
	res := map[string]any{
		"accountId": a.id, "queryState": a.stateLocked("Email"), "canCalculateChanges": false,
		"position": pos, "ids": ids,
	}
	if b, _ := args["calculateTotal"].(bool); b {
		res["total"] = len(hits)
	}
	return res
}

// emailMatches 实现客户端用到的 Email/query 条件: 邮箱、关键字、文本 (主题/发件人/正文包含)、AND/OR/NOT。
func emailMatches(m, f map[string]any) bool {
	if f == nil {
		return true
	}
	if op, ok := f["operator"].(string); ok {
		conds, _ := f["conditions"].([]any)
		switch op {
		case "AND":
			for _, c := range conds {
				if cm, _ := c.(map[string]any); !emailMatches(m, cm) {
					return false
				}
			}
			return true
		case "OR":
			for _, c := range conds {
				if cm, _ := c.(map[string]any); emailMatches(m, cm) {
					return true
				}
			}
			return false
		case "NOT":
			for _, c := range conds {
				if cm, _ := c.(map[string]any); emailMatches(m, cm) {
					return false
				}
			}
			return true
		}
	}
	boxes, _ := m["mailboxIds"].(map[string]any)
	kw, _ := m["keywords"].(map[string]any)
	if v, ok := f["inMailbox"].(string); ok && boxes[v] != true {
		return false
	}
	if list, ok := f["inMailboxOtherThan"].([]any); ok {
		for _, b := range list {
			if s, _ := b.(string); boxes[s] == true && len(boxes) == 1 {
				return false
			}
		}
	}
	if v, ok := f["hasKeyword"].(string); ok && kw[v] != true {
		return false
	}
	if v, ok := f["notKeyword"].(string); ok && kw[v] == true {
		return false
	}
	subject, _ := m["subject"].(string)
	from, _ := json.Marshal(m["from"])
	to, _ := json.Marshal(m["to"])
	contains := func(hay, needle string) bool {
		return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
	}
	if v, ok := f["subject"].(string); ok && !contains(subject, v) {
		return false
	}
	if v, ok := f["from"].(string); ok && !contains(string(from), v) {
		return false
	}
	if v, ok := f["to"].(string); ok && !contains(string(to), v) {
		return false
	}
	if v, ok := f["body"].(string); ok && !contains(bodyText(m), v) {
		return false
	}
	if v, ok := f["text"].(string); ok && !contains(subject+string(from)+string(to)+bodyText(m), v) {
		return false
	}
	return true
}

// setLocked 是通用的 /set: create / update (补丁, 支持 "a/b" 路径) / destroy。Email 与 Mailbox 额外维护邮箱计数。
func (ex *exchange) setLocked(acc *Account, typ string, args map[string]any) (map[string]any, methodError) {
	oldState := acc.stateLocked(typ)
	if want, ok := args["ifInState"].(string); ok && want != oldState {
		return nil, "stateMismatch"
	}
	created, notCreated := map[string]any{}, map[string]any{}
	updated, notUpdated := map[string]any{}, map[string]any{}
	destroyed, notDestroyed := []any{}, map[string]any{}

	if create, ok := args["create"].(map[string]any); ok {
		keys := sortedKeys(create)
		for _, cid := range keys {
			obj, _ := create[cid].(map[string]any)
			if obj == nil {
				notCreated[cid] = map[string]any{"type": "invalidArguments"}
				continue
			}
			id := acc.newID(strings.ToLower(typ[:1]))
			if typ == "Email" {
				obj = ex.newEmailLocked(acc, id, obj)
			}
			acc.putLocked(typ, id, obj)
			if typ == "Email" {
				acc.touchMailboxesLocked(mailboxesOf(obj)...)
			}
			ex.created[cid] = id
			info := map[string]any{"id": id}
			if typ == "Email" {
				info["blobId"], info["threadId"], info["size"] = obj["blobId"], obj["threadId"], obj["size"]
			}
			created[cid] = info
		}
	}
	if update, ok := args["update"].(map[string]any); ok {
		for _, rawID := range sortedKeys(update) {
			id := ex.ref(rawID)
			obj := acc.getLocked(typ, id)
			if obj == nil {
				notUpdated[rawID] = map[string]any{"type": "notFound"}
				continue
			}
			patch, _ := update[rawID].(map[string]any)
			before := mailboxesOf(obj)
			applyPatch(obj, patch)
			acc.touchLocked(typ, id)
			if typ == "Email" {
				acc.touchMailboxesLocked(append(before, mailboxesOf(obj)...)...)
			}
			updated[id] = nil
		}
	}
	if destroy, ok := args["destroy"].([]any); ok {
		for _, raw := range destroy {
			id := ex.ref(raw.(string))
			obj := acc.getLocked(typ, id)
			if obj == nil {
				notDestroyed[id] = map[string]any{"type": "notFound"}
				continue
			}
			boxes := mailboxesOf(obj)
			acc.destroyLocked(typ, id)
			if typ == "Email" {
				acc.touchMailboxesLocked(boxes...)
			}
			destroyed = append(destroyed, id)
		}
	}
	if len(created)+len(updated)+len(destroyed) > 0 {
		ex.touched = append(ex.touched, acc)
	}
	return map[string]any{
		"accountId": acc.id, "oldState": oldState, "newState": acc.stateLocked(typ),
		"created": created, "notCreated": notCreated, "updated": updated, "notUpdated": notUpdated,
		"destroyed": destroyed, "notDestroyed": notDestroyed,
	}, ""
}

// ref 把 "#创建 id" 换成本请求里新建对象的 id。
func (ex *exchange) ref(id string) string {
	if cid, ok := strings.CutPrefix(id, "#"); ok {
		if real, ok := ex.created[cid]; ok {
			return real
		}
	}
	return id
}

// newEmailLocked 给客户端新建的邮件 (草稿、发信) 补上服务器生成的属性。
func (ex *exchange) newEmailLocked(acc *Account, id string, obj map[string]any) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	obj["blobId"] = id + "-raw"
	if _, ok := obj["threadId"]; !ok {
		obj["threadId"] = "t" + id
	}
	if _, ok := obj["receivedAt"]; !ok {
		obj["receivedAt"] = now
	}
	if _, ok := obj["sentAt"]; !ok {
		obj["sentAt"] = now
	}
	if _, ok := obj["keywords"].(map[string]any); !ok {
		obj["keywords"] = map[string]any{}
	}
	if _, ok := obj["mailboxIds"].(map[string]any); !ok {
		obj["mailboxIds"] = map[string]any{}
	}
	for _, k := range []string{"from", "to", "cc", "bcc", "replyTo"} {
		if _, ok := obj[k].([]any); !ok {
			obj[k] = []any{}
		}
	}
	text := bodyText(obj)
	obj["size"] = len(text)
	obj["preview"] = preview(text)
	obj["messageId"] = []any{id + "@jmaptest"}
	if _, ok := obj["hasAttachment"]; !ok {
		obj["hasAttachment"] = false
	}
	if _, ok := obj["bodyStructure"]; !ok {
		obj["bodyStructure"] = map[string]any{"partId": "1", "type": "text/plain"}
	}
	if _, ok := obj["attachments"]; !ok {
		obj["attachments"] = []any{}
	}
	return obj
}

// applyPatch 应用 PatchObject: 键是 JSON Pointer (省略开头的 "/"), 值为 null 表示删除。
func applyPatch(obj, patch map[string]any) {
	for path, v := range patch {
		parts := strings.Split(path, "/")
		cur := obj
		for _, p := range parts[:len(parts)-1] {
			next, _ := cur[p].(map[string]any)
			if next == nil {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
		last := parts[len(parts)-1]
		if v == nil && len(parts) > 1 {
			delete(cur, last)
		} else {
			cur[last] = v
		}
	}
}

func (ex *exchange) importEmailsLocked(acc *Account, args map[string]any) map[string]any {
	emails, _ := args["emails"].(map[string]any)
	created := map[string]any{}
	for _, cid := range sortedKeys(emails) {
		spec, _ := emails[cid].(map[string]any)
		id := acc.newID("m")
		obj := ex.newEmailLocked(acc, id, map[string]any{
			"mailboxIds": spec["mailboxIds"], "keywords": spec["keywords"], "subject": "(imported)",
		})
		acc.putLocked("Email", id, obj)
		acc.touchMailboxesLocked(mailboxesOf(obj)...)
		ex.created[cid] = id
		created[cid] = map[string]any{"id": id, "blobId": obj["blobId"], "threadId": obj["threadId"], "size": obj["size"]}
	}
	if len(created) > 0 {
		ex.touched = append(ex.touched, acc)
	}
	return map[string]any{"accountId": acc.id, "oldState": nil, "newState": acc.stateLocked("Email"), "created": created, "notCreated": map[string]any{}}
}

// submitLocked 处理 EmailSubmission/set 的 create: 记下发出的邮件, 再按 onSuccessUpdateEmail / onSuccessDestroyEmail
// 改原邮件, 并像 RFC 8621 7.5 那样追加一个隐式的 Email/set 响应。
func (ex *exchange) submitLocked(acc *Account, args map[string]any, extra *[]invocation) (map[string]any, methodError) {
	created, notCreated := map[string]any{}, map[string]any{}
	subByCID := map[string]string{}
	create, _ := args["create"].(map[string]any)
	for _, cid := range sortedKeys(create) {
		spec, _ := create[cid].(map[string]any)
		emailID := ex.ref(stringArg(spec, "emailId"))
		m := acc.getLocked("Email", emailID)
		identityID := stringArg(spec, "identityId")
		if m == nil {
			notCreated[cid] = map[string]any{"type": "invalidProperties", "description": "email not found"}
			continue
		}
		if acc.getLocked("Identity", identityID) == nil {
			notCreated[cid] = map[string]any{"type": "invalidProperties", "description": "identity not found"}
			continue
		}
		id := acc.newID("sub")
		acc.putLocked("EmailSubmission", id, map[string]any{"identityId": identityID, "emailId": emailID, "undoStatus": "final"})
		subByCID[cid] = id
		ex.created[cid] = id
		created[cid] = map[string]any{"id": id, "undoStatus": "final"}
		sub := Sent{User: ex.u.name, AccountID: acc.id, IdentityID: identityID, EmailID: emailID}
		sub.Subject, _ = m["subject"].(string)
		sub.From = addresses(m["from"])
		sub.To = addresses(m["to"])
		ex.s.submissions = append(ex.s.submissions, sub)
	}
	res := map[string]any{
		"accountId": acc.id, "oldState": nil, "newState": acc.stateLocked("EmailSubmission"),
		"created": created, "notCreated": notCreated, "updated": map[string]any{}, "notUpdated": map[string]any{},
		"destroyed": []any{}, "notDestroyed": map[string]any{},
	}
	if update, ok := args["update"].(map[string]any); ok {
		for id := range update {
			res["updated"].(map[string]any)[id] = nil
		}
	}

	onUpdate, _ := args["onSuccessUpdateEmail"].(map[string]any)
	onDestroy, _ := args["onSuccessDestroyEmail"].([]any)
	if len(onUpdate) == 0 && len(onDestroy) == 0 {
		return res, ""
	}
	emailFor := func(ref string) string {
		subID := ref
		if cid, ok := strings.CutPrefix(ref, "#"); ok {
			subID = subByCID[cid]
		}
		if sub := acc.getLocked("EmailSubmission", subID); sub != nil {
			id, _ := sub["emailId"].(string)
			return id
		}
		return ""
	}
	set := map[string]any{"accountId": acc.id}
	if len(onUpdate) > 0 {
		upd := map[string]any{}
		for ref, patch := range onUpdate {
			if id := emailFor(ref); id != "" {
				upd[id] = patch
			}
		}
		set["update"] = upd
	}
	if len(onDestroy) > 0 {
		var ids []any
		for _, ref := range onDestroy {
			if id := emailFor(ref.(string)); id != "" {
				ids = append(ids, id)
			}
		}
		set["destroy"] = ids
	}
	emailRes, _ := ex.setLocked(acc, "Email", set)
	*extra = append(*extra, invocation{name: "Email/set", args: emailRes})
	return res, ""
}

func addresses(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			if e, ok := m["email"].(string); ok {
				out = append(out, e)
			}
		}
	}
	return out
}

func stringArg(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func numArg(m map[string]any, k string) float64 {
	f, _ := m[k].(float64)
	return f
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
