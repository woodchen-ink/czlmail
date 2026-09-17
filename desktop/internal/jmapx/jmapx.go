// Package jmapx 补齐 go-jmap 未实现的 JMAP 扩展: 日历、通讯录、文件节点。
//
// 这三类对象(JSCalendar RFC 8984 / JSContact RFC 9553 / FileNode)字段深且开放,
// 服务端还在持续扩展。给每个属性写 Go 结构体既写不全, 也会在往返时丢掉不认识的字段。
// 因此方法层一律用通用的 /get /changes /set /query, 对象本体保持 json.RawMessage,
// 由上层按需抽取字段, 写回时只发补丁。
//
// go-jmap 按方法名查响应工厂, 在 init 里注册即可复用它的请求编排与 back-reference。
package jmapx

import (
	"encoding/json"

	"git.sr.ht/~rockorager/go-jmap"
)

// 能力 URI。取值与 Stalwart 会话里公布的一致。
const (
	CalendarsURI jmap.URI = "urn:ietf:params:jmap:calendars"
	ContactsURI  jmap.URI = "urn:ietf:params:jmap:contacts"
	FileNodeURI  jmap.URI = "urn:ietf:params:jmap:filenode"

	SieveURI          jmap.URI = "urn:ietf:params:jmap:sieve"
	PrincipalsURI     jmap.URI = "urn:ietf:params:jmap:principals"
	CalendarsParseURI jmap.URI = "urn:ietf:params:jmap:calendars:parse"
	ContactsParseURI  jmap.URI = "urn:ietf:params:jmap:contacts:parse"
)

// 对象类型名, 也是推送 StateChange 里的 key。
const (
	Calendar      = "Calendar"
	CalendarEvent = "CalendarEvent"
	AddressBook   = "AddressBook"
	ContactCard   = "ContactCard"
	FileNode      = "FileNode"
)

// URIFor 返回某类型所属的能力。
func URIFor(typeName string) jmap.URI {
	switch typeName {
	case Calendar, CalendarEvent:
		return CalendarsURI
	case AddressBook, ContactCard:
		return ContactsURI
	case "Email":
		return "urn:ietf:params:jmap:mail"
	case "SieveScript":
		return SieveURI
	case "Principal":
		return PrincipalsURI
	default:
		return FileNodeURI
	}
}

func init() {
	for _, t := range []string{Calendar, CalendarEvent, AddressBook, ContactCard, FileNode} {
		jmap.RegisterMethod(t+"/get", func() jmap.MethodResponse { return &GetResponse{} })
		jmap.RegisterMethod(t+"/changes", func() jmap.MethodResponse { return &ChangesResponse{} })
		jmap.RegisterMethod(t+"/set", func() jmap.MethodResponse { return &SetResponse{} })
		jmap.RegisterMethod(t+"/query", func() jmap.MethodResponse { return &QueryResponse{} })
	}
	jmap.RegisterMethod(CalendarEvent+"/parse", func() jmap.MethodResponse { return &ParseResponse{} })
	jmap.RegisterMethod("SieveScript/get", func() jmap.MethodResponse { return &GetResponse{} })
	jmap.RegisterMethod("Principal/get", func() jmap.MethodResponse { return &GetResponse{} })
	jmap.RegisterMethod("SieveScript/set", func() jmap.MethodResponse { return &SetResponse{} })
	jmap.RegisterMethod("SieveScript/validate", func() jmap.MethodResponse { return &ValidateResponse{} })
	jmap.RegisterMethod(ContactCard+"/parse", func() jmap.MethodResponse { return &ParseResponse{} })
}

// Call 是一次通用方法调用。Args 原样序列化为参数对象。
type Call struct {
	Method string
	Args   map[string]any
}

func (c *Call) Name() string { return c.Method }

func (c *Call) Requires() []jmap.URI {
	switch c.Method {
	case CalendarEvent + "/parse":
		return []jmap.URI{CalendarsURI, CalendarsParseURI}
	case ContactCard + "/parse":
		return []jmap.URI{ContactsURI, ContactsParseURI}
	}
	for i := range c.Method {
		if c.Method[i] == '/' {
			return []jmap.URI{URIFor(c.Method[:i])}
		}
	}
	return nil
}

func (c *Call) MarshalJSON() ([]byte, error) { return json.Marshal(c.Args) }

// Get 构造 T/get。ids 为 nil 时取全部。
func Get(typeName, accountID string, ids []string) *Call {
	args := map[string]any{"accountId": accountID}
	if ids != nil {
		args["ids"] = ids
	}
	return &Call{Method: typeName + "/get", Args: args}
}

// Changes 构造 T/changes。
func Changes(typeName, accountID, since string, max int) *Call {
	return &Call{Method: typeName + "/changes", Args: map[string]any{
		"accountId": accountID, "sinceState": since, "maxChanges": max,
	}}
}

// Set 构造 T/set。create / update / destroy 为空时省略。extra 合入额外参数。
func Set(typeName, accountID string, create map[string]any, update map[string]map[string]any, destroy []string, extra map[string]any) *Call {
	args := map[string]any{"accountId": accountID}
	if len(create) > 0 {
		args["create"] = create
	}
	if len(update) > 0 {
		args["update"] = update
	}
	if len(destroy) > 0 {
		args["destroy"] = destroy
	}
	for k, v := range extra {
		args[k] = v
	}
	return &Call{Method: typeName + "/set", Args: args}
}

type GetResponse struct {
	AccountID string            `json:"accountId"`
	State     string            `json:"state"`
	List      []json.RawMessage `json:"list"`
	NotFound  []string          `json:"notFound"`
}

type ChangesResponse struct {
	AccountID      string   `json:"accountId"`
	OldState       string   `json:"oldState"`
	NewState       string   `json:"newState"`
	HasMoreChanges bool     `json:"hasMoreChanges"`
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Destroyed      []string `json:"destroyed"`
}

type SetError struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Properties  []string `json:"properties"`
}

func (e *SetError) Error() string {
	if e.Description != "" {
		return e.Type + ": " + e.Description
	}
	return e.Type
}

type SetResponse struct {
	AccountID    string                     `json:"accountId"`
	OldState     string                     `json:"oldState"`
	NewState     string                     `json:"newState"`
	Created      map[string]json.RawMessage `json:"created"`
	Updated      map[string]json.RawMessage `json:"updated"`
	Destroyed    []string                   `json:"destroyed"`
	NotCreated   map[string]*SetError       `json:"notCreated"`
	NotUpdated   map[string]*SetError       `json:"notUpdated"`
	NotDestroyed map[string]*SetError       `json:"notDestroyed"`
}

// FirstError 返回响应里的第一个失败项。/set 整体成功不代表每一项都成功。
func (r *SetResponse) FirstError() error {
	for _, m := range []map[string]*SetError{r.NotCreated, r.NotUpdated, r.NotDestroyed} {
		for _, e := range m {
			if e != nil {
				return e
			}
		}
	}
	return nil
}

type QueryResponse struct {
	AccountID  string   `json:"accountId"`
	QueryState string   `json:"queryState"`
	Position   int      `json:"position"`
	IDs        []string `json:"ids"`
	Total      int      `json:"total"`
}

// ParseResponse 是 T/parse 的结果: 每个 blob 解析出的对象列表(ics/vcf 可含多条)。
type ParseResponse struct {
	AccountID   string                     `json:"accountId"`
	Parsed      map[string]json.RawMessage `json:"parsed"`
	NotParsable []string                   `json:"notParsable"`
	NotFound    []string                   `json:"notFound"`
}

// ValidateResponse 是 SieveScript/validate 的结果, Error 为 nil 表示脚本合法。
type ValidateResponse struct {
	AccountID string    `json:"accountId"`
	Error     *SetError `json:"error"`
}
