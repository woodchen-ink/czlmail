package jmaptest

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Account 是服务器上的一个 JMAP 账号, 可以同时被多个用户看到 (共享)。
type Account struct {
	s    *Server
	id   string
	name string
	caps []string
	// capObjs 是测试指定的能力对象 (如网关的上游状态), 覆盖 accountCapability 的默认值。
	capObjs map[string]any
	// unavailable 非空时依赖上游的方法返回 serverUnavailable 附 czlUpstream, 下载回 503 (见 SetUnavailable)。
	unavailable map[string]any
	// stamp 是本账号的变更计数, 各类型的 state 都取自它, 单调递增。
	stamp int64
	types map[string]*collection
}

// collection 是一个类型的全部对象与变更记录。对象一律是 JSON 对象 (map), 不认识的属性原样保存。
type collection struct {
	state int64
	objs  map[string]*object
	// gone 是删除过的对象: 删除时的 stamp 与创建时的 stamp, 供 /changes 判断。
	gone map[string][2]int64
}

type object struct {
	created, updated int64
	data             map[string]any
}

// 默认建的邮箱, id 与 role 同名。
var defaultMailboxes = []struct{ id, name, role string }{
	{"inbox", "Inbox", "inbox"},
	{"drafts", "Drafts", "drafts"},
	{"sent", "Sent", "sent"},
	{"archive", "Archive", "archive"},
	{"junk", "Junk", "junk"},
	{"trash", "Trash", "trash"},
}

// AddAccount 加一个账号, caps 为空时用 DefaultCaps。会建好 inbox / drafts / sent / archive / junk / trash
// 六个邮箱 (id 即 role) 与一个发件身份 (id "<账号 id>-identity", 地址是 name, name 不像地址时是 "<name>@jmaptest.invalid")。
// 账号 id 在一台服务器内唯一; 不同服务器可以重复, 用来测账号 id 撞车。
func (s *Server) AddAccount(id, name string, caps ...string) *Account {
	if len(caps) == 0 {
		caps = DefaultCaps
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc := &Account{s: s, id: id, name: name, caps: slices.Clone(caps), types: map[string]*collection{}}
	s.accounts[id] = acc
	for i, mb := range defaultMailboxes {
		acc.putLocked("Mailbox", mb.id, map[string]any{
			"name": mb.name, "role": mb.role, "parentId": nil, "sortOrder": i, "isSubscribed": true,
		})
	}
	email := name
	if !strings.Contains(email, "@") {
		email = name + "@jmaptest.invalid"
	}
	acc.putLocked("Identity", id+"-identity", map[string]any{
		"name": name, "email": email, "replyTo": nil, "bcc": nil,
		"textSignature": "", "htmlSignature": "", "mayDelete": false,
	})
	s.sessionGen++
	return acc
}

// SetIdentities 把账号的发件身份换成这些地址 (id 依次为 "<账号 id>-identity", "<账号 id>-identity2", ...)。
func (a *Account) SetIdentities(emails ...string) {
	a.s.mu.Lock()
	for id := range a.coll("Identity").objs {
		a.destroyLocked("Identity", id)
	}
	for i, email := range emails {
		id := a.id + "-identity"
		if i > 0 {
			id += strconv.Itoa(i + 1)
		}
		a.putLocked("Identity", id, map[string]any{
			"name": a.name, "email": email, "replyTo": nil, "bcc": nil,
			"textSignature": "", "htmlSignature": "", "mayDelete": false,
		})
	}
	a.s.mu.Unlock()
}

// ID 是服务器上的账号 id。
func (a *Account) ID() string { return a.id }

// SetCaps 改账号的能力 (下一次读会话生效)。
func (a *Account) SetCaps(caps ...string) {
	a.s.mu.Lock()
	a.caps = slices.Clone(caps)
	a.s.sessionGen++
	a.s.mu.Unlock()
}

// SetCapability 设定账号某能力的对象 (如网关的 Upstream 状态), 没有这项能力时加上; obj 为 nil 时去掉这项能力。
// 改了会话 state (sessionGen), 下一次读会话与之后的 API 响应 (sessionState) 都能看到; 不断开推送, 要模拟网关断流用 DropStreams。
func (a *Account) SetCapability(uri string, obj any) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if obj == nil {
		a.caps = slices.DeleteFunc(a.caps, func(c string) bool { return c == uri })
		delete(a.capObjs, uri)
	} else {
		if !slices.Contains(a.caps, uri) {
			a.caps = append(a.caps, uri)
		}
		if a.capObjs == nil {
			a.capObjs = map[string]any{}
		}
		a.capObjs[uri] = obj
	}
	a.s.sessionGen++
}

// SetUnavailable 模拟网关的上游邮箱不可用: 之后这个账号上的方法都返回 serverUnavailable 附
// czlUpstream {status, code}, 下载回 503 problem JSON; status 为空时恢复。只管方法结果, 账号能力里的上游状态另用 SetCapability 改。
func (a *Account) SetUnavailable(status string, code int) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if status == "" {
		a.unavailable = nil
		return
	}
	a.unavailable = map[string]any{"status": status, "code": code}
}

// Rename 改账号名称 (下一次读会话生效)。
func (a *Account) Rename(name string) {
	a.s.mu.Lock()
	a.name = name
	a.s.sessionGen++
	a.s.mu.Unlock()
}

func (a *Account) coll(typ string) *collection {
	c := a.types[typ]
	if c == nil {
		c = &collection{objs: map[string]*object{}, gone: map[string][2]int64{}}
		a.types[typ] = c
	}
	return c
}

func (a *Account) bump(c *collection) int64 {
	a.stamp++
	c.state = a.stamp
	return a.stamp
}

// putLocked 新建或整体替换一个对象。
func (a *Account) putLocked(typ, id string, data map[string]any) {
	c := a.coll(typ)
	st := a.bump(c)
	data["id"] = id
	if o, ok := c.objs[id]; ok {
		o.data, o.updated = data, st
		return
	}
	c.objs[id] = &object{created: st, updated: st, data: data}
	delete(c.gone, id)
}

func (a *Account) touchLocked(typ, id string) {
	c := a.coll(typ)
	if o, ok := c.objs[id]; ok {
		o.updated = a.bump(c)
	}
}

func (a *Account) destroyLocked(typ, id string) bool {
	c := a.coll(typ)
	o, ok := c.objs[id]
	if !ok {
		return false
	}
	st := a.bump(c)
	c.gone[id] = [2]int64{o.created, st}
	delete(c.objs, id)
	return true
}

func (a *Account) getLocked(typ, id string) map[string]any {
	if o, ok := a.coll(typ).objs[id]; ok {
		return o.data
	}
	return nil
}

func (a *Account) stateLocked(typ string) string {
	return strconv.FormatInt(a.coll(typ).state, 10)
}

// newID 分配一个账号内未用过的 id (测试可能已经用 Email.ID 指定过 "m1" 这种)。调用方持有 s.mu。
func (a *Account) newID(prefix string) string {
	for {
		a.s.seq++
		id := fmt.Sprintf("%s%d", prefix, a.s.seq)
		taken := false
		for _, c := range a.types {
			if _, ok := c.objs[id]; ok {
				taken = true
			} else if _, ok := c.gone[id]; ok {
				taken = true
			}
		}
		if !taken {
			return id
		}
	}
}

// Email 是 AddEmail 的参数。零值字段取默认: 收件箱、未读、此刻收到。
type Email struct {
	// ID 为空时自动分配。不同账号、不同服务器上可以重复 (测撞车)。
	ID      string
	Subject string
	From    string
	To      []string
	// Mailbox 是邮箱 id, 默认 "inbox"。
	Mailbox    string
	Seen       bool
	Flagged    bool
	ReceivedAt time.Time
	Body       string
	// Attachments 是附件文件名, 内容是文件名本身。
	Attachments []string
	ThreadID    string
}

// AddEmail 放进一封邮件, 返回邮件 id。
func (a *Account) AddEmail(e Email) string {
	a.s.mu.Lock()
	id := a.addEmailLocked(e)
	a.s.mu.Unlock()
	a.s.changed(a)
	return id
}

func (a *Account) addEmailLocked(e Email) string {
	if e.ID == "" {
		e.ID = a.newID("m")
	}
	if e.Mailbox == "" {
		e.Mailbox = "inbox"
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = time.Now()
	}
	if e.ThreadID == "" {
		e.ThreadID = "t" + e.ID
	}
	keywords := map[string]any{}
	if e.Seen {
		keywords["$seen"] = true
	}
	if e.Flagged {
		keywords["$flagged"] = true
	}
	to := []any{}
	for _, addr := range e.To {
		to = append(to, map[string]any{"name": nil, "email": addr})
	}
	body := e.Body
	if body == "" {
		body = "Body of " + e.Subject
	}
	textPart := map[string]any{"partId": "1", "type": "text/plain", "disposition": nil, "name": nil, "size": len(body)}
	structure := textPart
	attachments := []any{}
	if len(e.Attachments) > 0 {
		sub := []any{textPart}
		for i, name := range e.Attachments {
			part := map[string]any{
				"partId": strconv.Itoa(i + 2), "blobId": e.ID + "-att" + strconv.Itoa(i), "type": "application/octet-stream",
				"disposition": "attachment", "name": name, "size": len(name),
			}
			sub = append(sub, part)
			attachments = append(attachments, part)
		}
		structure = map[string]any{"partId": nil, "type": "multipart/mixed", "subParts": sub}
	}
	a.putLocked("Email", e.ID, map[string]any{
		"blobId": e.ID + "-raw", "threadId": e.ThreadID,
		"mailboxIds": map[string]any{e.Mailbox: true}, "keywords": keywords,
		"size": len(body), "receivedAt": e.ReceivedAt.UTC().Format(time.RFC3339), "sentAt": e.ReceivedAt.UTC().Format(time.RFC3339),
		"from": []any{map[string]any{"name": nil, "email": e.From}},
		"to":   to,
		"cc":   []any{}, "bcc": []any{}, "replyTo": []any{},
		"subject": e.Subject, "preview": preview(body), "hasAttachment": len(e.Attachments) > 0,
		"messageId": []any{e.ID + "@jmaptest"}, "inReplyTo": nil, "references": nil,
		"bodyStructure": structure,
		"textBody":      []any{textPart}, "htmlBody": []any{textPart},
		"bodyValues":  map[string]any{"1": map[string]any{"value": body, "isEncodingProblem": false, "isTruncated": false}},
		"attachments": attachments,
		"headers":     []any{},
	})
	a.touchMailboxesLocked(e.Mailbox)
	return e.ID
}

func preview(body string) string {
	if len(body) > 120 {
		return body[:120]
	}
	return body
}

// SetKeyword 设或清一封邮件的关键字 (如 "$seen")。
func (a *Account) SetKeyword(emailID, keyword string, on bool) {
	a.s.mu.Lock()
	if m := a.getLocked("Email", emailID); m != nil {
		kw, _ := m["keywords"].(map[string]any)
		if kw == nil {
			kw = map[string]any{}
			m["keywords"] = kw
		}
		if on {
			kw[keyword] = true
		} else {
			delete(kw, keyword)
		}
		a.touchLocked("Email", emailID)
		a.touchMailboxesLocked(mailboxesOf(m)...)
	}
	a.s.mu.Unlock()
	a.s.changed(a)
}

// DestroyEmail 删掉一封邮件 (别的客户端删的)。
func (a *Account) DestroyEmail(emailID string) {
	a.s.mu.Lock()
	m := a.getLocked("Email", emailID)
	if m != nil {
		boxes := mailboxesOf(m)
		a.destroyLocked("Email", emailID)
		a.touchMailboxesLocked(boxes...)
	}
	a.s.mu.Unlock()
	a.s.changed(a)
}

// Object 返回某类型对象的当前内容 (副本), 供断言 PIM、发件身份、自动回复 ("VacationResponse"/"singleton") 等。
func (a *Account) Object(typ, id string) (map[string]any, bool) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	m := a.getLocked(typ, id)
	if m == nil {
		return nil, false
	}
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, true
}

// MailboxNames 返回账号里全部邮箱的名称, 排序。
func (a *Account) MailboxNames() []string {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	var out []string
	for _, o := range a.coll("Mailbox").objs {
		if name, ok := o.data["name"].(string); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// EmailInfo 是一封邮件在服务器上的当前状态, 供断言。
type EmailInfo struct {
	ID        string
	Subject   string
	Mailboxes []string
	Keywords  []string
}

// Emails 返回账号里的全部邮件, 按 id 排序。
func (a *Account) Emails() []EmailInfo {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	var out []EmailInfo
	for id, o := range a.coll("Email").objs {
		out = append(out, emailInfo(id, o.data))
	}
	slices.SortFunc(out, func(x, y EmailInfo) int { return strings.Compare(x.ID, y.ID) })
	return out
}

// EmailByID 返回一封邮件的当前状态, 不存在时 ok 为假。
func (a *Account) EmailByID(id string) (EmailInfo, bool) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	m := a.getLocked("Email", id)
	if m == nil {
		return EmailInfo{}, false
	}
	return emailInfo(id, m), true
}

func emailInfo(id string, m map[string]any) EmailInfo {
	info := EmailInfo{ID: id, Mailboxes: mailboxesOf(m)}
	info.Subject, _ = m["subject"].(string)
	if kw, ok := m["keywords"].(map[string]any); ok {
		info.Keywords = slices.Sorted(maps.Keys(kw))
	}
	return info
}

func mailboxesOf(m map[string]any) []string {
	boxes, _ := m["mailboxIds"].(map[string]any)
	out := make([]string, 0, len(boxes))
	for id, v := range boxes {
		if v == true {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (a *Account) touchMailboxesLocked(ids ...string) {
	for _, id := range ids {
		a.touchLocked("Mailbox", id)
	}
}

// mailboxView 在 Mailbox 对象上补上按当前邮件算出的计数与权限。
func (a *Account) mailboxViewLocked(m map[string]any) map[string]any {
	out := maps.Clone(m)
	id, _ := m["id"].(string)
	total, unread := 0, 0
	threads, unreadThreads := map[string]bool{}, map[string]bool{}
	for _, o := range a.coll("Email").objs {
		boxes, _ := o.data["mailboxIds"].(map[string]any)
		if boxes[id] != true {
			continue
		}
		total++
		tid, _ := o.data["threadId"].(string)
		threads[tid] = true
		kw, _ := o.data["keywords"].(map[string]any)
		if kw["$seen"] != true {
			unread++
			unreadThreads[tid] = true
		}
	}
	out["totalEmails"], out["unreadEmails"] = total, unread
	out["totalThreads"], out["unreadThreads"] = len(threads), len(unreadThreads)
	rights := map[string]any{}
	for _, r := range []string{"mayReadItems", "mayAddItems", "mayRemoveItems", "maySetSeen", "maySetKeywords", "mayCreateChild", "mayRename", "mayDelete", "maySubmit"} {
		rights[r] = true
	}
	out["myRights"] = rights
	return out
}

// rawMessageLocked 给邮件的 blobId 拼一份最简单的原文, 供下载 (查看原文、导出)。
func (a *Account) rawMessageLocked(blobID string) ([]byte, bool) {
	for _, o := range a.coll("Email").objs {
		if o.data["blobId"] != blobID {
			continue
		}
		subject, _ := o.data["subject"].(string)
		raw := fmt.Sprintf("Subject: %s\r\nMessage-ID: <%v>\r\n\r\n%s\r\n", subject, o.data["id"], bodyText(o.data))
		return []byte(raw), true
	}
	return nil, false
}

func bodyText(m map[string]any) string {
	values, _ := m["bodyValues"].(map[string]any)
	var parts []string
	for _, v := range values {
		if bv, ok := v.(map[string]any); ok {
			if s, ok := bv["value"].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n")
}

// statePayloadLocked 是账号全部类型当前 state 的 StateChange。调用方持有 s.mu。
func statePayloadLocked(a *Account) []byte {
	states := map[string]string{}
	for typ, c := range a.types {
		states[typ] = strconv.FormatInt(c.state, 10)
	}
	payload, _ := json.Marshal(map[string]any{
		"@type":   "StateChange",
		"changed": map[string]any{a.id: states},
	})
	return payload
}

// Push 立即向能看到这些账号的用户推送一次 StateChange (自动推送关掉时用)。
func (s *Server) Push(accounts ...*Account) {
	for _, a := range accounts {
		s.push(a)
	}
}

// changed 在数据变化后按 autoPush 推送。
func (s *Server) changed(a *Account) {
	s.mu.Lock()
	on := s.autoPush
	s.mu.Unlock()
	if on {
		s.push(a)
	}
}

func (s *Server) push(a *Account) {
	s.mu.Lock()
	payload := statePayloadLocked(a)
	for st := range s.streams {
		if _, ok := st.user.sees(a); ok {
			st.send(payload)
		}
	}
	s.mu.Unlock()
}
