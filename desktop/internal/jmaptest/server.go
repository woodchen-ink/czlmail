// Package jmaptest 是测试用的内存 JMAP 服务器 (基于 httptest), 只在测试里 import, 不进正式程序。
//
// 一个 Server 是一台邮件服务器: 多个用户 (Basic 认证), 每个用户能看到若干账号 (个人 + 共享),
// 账号 id 由测试指定 (两台服务器都用 "a" 就能让账号 id 撞车), 每个账号声明自己的能力。
// 实现了客户端实际会发的方法: 会话资源、Mailbox / Email / Identity / EmailSubmission / VacationResponse,
// 日历、通讯录、文件等按能力声明的类型走通用的 get / changes / query / set, 以及 EventSource 推送、blob 上传下载。
//
// 状态由测试控制: 加信、改已读、删信 (默认自动推送 StateChange), 离线 (503 或直接断连接),
// 拒绝某个用户的凭据 (401), 增删共享账号。客户端发来的每个方法调用都记在 Calls 里,
// 可以据此断言请求发到了哪台服务器、用的是哪个服务器账号 id; OnMethod 钩子可以在方法执行前卡住它, 用来复现竞态。
//
// 用法见 example_test.go。
package jmaptest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// 能力 URI, 与 Stalwart 会话里公布的一致。
const (
	Core             = "urn:ietf:params:jmap:core"
	Mail             = "urn:ietf:params:jmap:mail"
	Submission       = "urn:ietf:params:jmap:submission"
	Calendars        = "urn:ietf:params:jmap:calendars"
	Contacts         = "urn:ietf:params:jmap:contacts"
	FileNode         = "urn:ietf:params:jmap:filenode"
	Sieve            = "urn:ietf:params:jmap:sieve"
	VacationResponse = "urn:ietf:params:jmap:vacationresponse"
	Principals       = "urn:ietf:params:jmap:principals"
)

// DefaultCaps 是 AddAccount 不传能力时的默认值: 收发邮件。
var DefaultCaps = []string{Mail, Submission}

// AllCaps 是全部能力, 相当于 Stalwart 的个人账号。
var AllCaps = []string{Mail, Submission, Calendars, Contacts, FileNode, Sieve, VacationResponse}

// Mode 是服务器的在线状态。
type Mode int

const (
	// Online 正常服务。
	Online Mode = iota
	// Unavailable 一切请求回 503 (维护中、反向代理后的服务挂了)。
	Unavailable
	// Down 直接断开连接, 客户端看到的是网络错误 (服务器关机、断网)。
	Down
)

// Call 是客户端发来的一个方法调用。
type Call struct {
	// User 是发起请求的用户名。
	User   string
	Method string
	// AccountID 是参数里的 accountId (服务器账号 id), 没有时为空。
	AccountID string
	Args      json.RawMessage
}

// Sent 是一次 EmailSubmission/set 创建的发信记录。
type Sent struct {
	User       string
	AccountID  string
	IdentityID string
	EmailID    string
	Subject    string
	From       []string
	To         []string
}

// Server 是一台内存 JMAP 服务器。方法都可以并发调用。
type Server struct {
	srv *httptest.Server

	mu          sync.Mutex
	users       map[string]*User
	accounts    map[string]*Account
	mode        Mode
	autoPush    bool
	calls       []Call
	submissions []Sent
	blobs       map[string][]byte
	seq         int64
	streams     map[*stream]struct{}
	onMethod    func(Call)
	sessionGen  int64
	closed      bool
}

// New 启动一台服务器, 测试结束时自动关闭。
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		users:    map[string]*User{},
		accounts: map[string]*Account{},
		autoPush: true,
		blobs:    map[string][]byte{},
		streams:  map[*stream]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jmap", s.guard(s.handleSession))
	mux.HandleFunc("/jmap/session", s.guard(s.handleSession))
	mux.HandleFunc("/jmap/api", s.guard(s.handleAPI))
	mux.HandleFunc("/jmap/eventsource", s.guard(s.handleEventSource))
	mux.HandleFunc("/jmap/upload/", s.guard(s.handleUpload))
	mux.HandleFunc("/jmap/download/", s.guard(s.handleDownload))
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// URL 是服务器地址 (http://127.0.0.1:端口), 登录时填它作服务器地址, 会话资源在 /.well-known/jmap。
func (s *Server) URL() string { return s.srv.URL }

// SessionURL 是会话资源地址。
func (s *Server) SessionURL() string { return s.srv.URL + "/.well-known/jmap" }

// Close 断开全部推送连接并关闭服务器。
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.dropStreamsLocked(func(*stream) bool { return true })
	s.mu.Unlock()
	s.srv.CloseClientConnections()
	s.srv.Close()
}

// SetMode 切换在线状态。离线时断开全部推送连接。
func (s *Server) SetMode(m Mode) {
	s.mu.Lock()
	s.mode = m
	if m != Online {
		s.dropStreamsLocked(func(*stream) bool { return true })
	}
	s.mu.Unlock()
	if m == Down {
		s.srv.CloseClientConnections()
	}
}

// SetAutoPush 设定数据变化后是否自动推送 StateChange (默认开)。关掉后用 Push 手动推。
func (s *Server) SetAutoPush(on bool) {
	s.mu.Lock()
	s.autoPush = on
	s.mu.Unlock()
}

// OnMethod 设置方法执行前的钩子, 在服务器锁外、按请求协程调用; 传 nil 取消。
// 钩子里阻塞就能把客户端卡在某个调用上 (比如让一次 Email/get 等到测试把登录移除了再回)。
func (s *Server) OnMethod(fn func(Call)) {
	s.mu.Lock()
	s.onMethod = fn
	s.mu.Unlock()
}

// Calls 返回至今收到的全部方法调用。
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// CallsTo 返回某方法的全部调用。
func (s *Server) CallsTo(method string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// ResetCalls 清空调用记录。
func (s *Server) ResetCalls() {
	s.mu.Lock()
	s.calls = nil
	s.mu.Unlock()
}

// Submissions 返回至今发出的邮件。
func (s *Server) Submissions() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.submissions)
}

// User 是服务器上的一个登录用户。
type User struct {
	s        *Server
	name     string
	password string
	rejected bool
	grants   []grant
}

type grant struct {
	acc      *Account
	personal bool
}

// AddUser 加一个用户。
func (s *Server) AddUser(username, password string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := &User{s: s, name: username, password: password}
	s.users[strings.ToLower(username)] = u
	return u
}

// Name 是用户名。
func (u *User) Name() string { return u.name }

// Grant 让用户能看到账号; personal 为真时是该用户的个人账号 (会话里 isPersonal, 也是 primaryAccounts)。
// 已经能看到时改个人/共享属性。会话变化不主动推送, 客户端在重读会话时看到。
func (u *User) Grant(acc *Account, personal bool) *User {
	s := u.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range u.grants {
		if u.grants[i].acc == acc {
			u.grants[i].personal = personal
			s.sessionGen++
			return u
		}
	}
	u.grants = append(u.grants, grant{acc: acc, personal: personal})
	s.sessionGen++
	return u
}

// Revoke 收回用户对账号的访问 (撤销共享)。
func (u *User) Revoke(acc *Account) {
	s := u.s
	s.mu.Lock()
	defer s.mu.Unlock()
	u.grants = slices.DeleteFunc(u.grants, func(g grant) bool { return g.acc == acc })
	s.sessionGen++
}

// Reject 为真时服务器拒绝这个用户的凭据 (一切请求回 401), 并断开他的推送连接, 模拟应用专用密码被撤销。
func (u *User) Reject(on bool) {
	s := u.s
	s.mu.Lock()
	u.rejected = on
	if on {
		s.dropStreamsLocked(func(st *stream) bool { return st.user == u })
	}
	s.mu.Unlock()
}

// SetPassword 改密码 (旧密码随即失效)。
func (u *User) SetPassword(password string) {
	s := u.s
	s.mu.Lock()
	u.password = password
	s.dropStreamsLocked(func(st *stream) bool { return st.user == u })
	s.mu.Unlock()
}

func (u *User) sees(acc *Account) (grant, bool) {
	for _, g := range u.grants {
		if g.acc == acc {
			return g, true
		}
	}
	return grant{}, false
}

// guard 处理在线状态与认证, 通过后把用户交给 next。
func (s *Server) guard(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		mode := s.mode
		s.mu.Unlock()
		switch mode {
		case Unavailable:
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		case Down:
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		u := s.authenticate(r)
		if u == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="jmaptest"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r, u)
	}
}

func (s *Server) authenticate(r *http.Request) *User {
	h := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(h, "Basic ")
	if !ok {
		return nil
	}
	dec, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	name, pass, ok := strings.Cut(string(dec), ":")
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[strings.ToLower(name)]
	if u == nil || u.rejected || u.password != pass {
		return nil
	}
	return u
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, u *User) {
	s.mu.Lock()
	body := s.sessionLocked(u)
	s.mu.Unlock()
	writeJSON(w, body)
}

func (s *Server) sessionLocked(u *User) map[string]any {
	base := s.srv.URL + "/jmap"
	caps := map[string]any{
		Core: map[string]any{
			"maxSizeUpload": 50000000, "maxConcurrentUpload": 4, "maxSizeRequest": 10000000,
			"maxConcurrentRequests": 4, "maxCallsInRequest": 16, "maxObjectsInGet": 500,
			"maxObjectsInSet": 500, "collationAlgorithms": []string{},
		},
		Mail: map[string]any{},
	}
	accounts := map[string]any{}
	primary := map[string]string{}
	for _, g := range u.grants {
		accCaps := map[string]any{}
		for _, c := range g.acc.caps {
			accCaps[c] = accountCapability(c)
			if _, ok := caps[c]; !ok {
				caps[c] = map[string]any{}
			}
		}
		accounts[g.acc.id] = map[string]any{
			"name": g.acc.name, "isPersonal": g.personal, "isReadOnly": false,
			"accountCapabilities": accCaps,
		}
		if g.personal {
			for _, c := range g.acc.caps {
				primary[c] = g.acc.id
			}
		}
	}
	return map[string]any{
		"capabilities":    caps,
		"accounts":        accounts,
		"primaryAccounts": primary,
		"username":        u.name,
		"apiUrl":          base + "/api",
		"downloadUrl":     base + "/download/{accountId}/{blobId}/{name}?type={type}",
		"uploadUrl":       base + "/upload/{accountId}",
		"eventSourceUrl":  base + "/eventsource?types={types}&closeafter={closeafter}&ping={ping}",
		"state":           strconv.FormatInt(s.sessionGen, 10),
	}
}

func accountCapability(uri string) any {
	switch uri {
	case Mail:
		return map[string]any{
			"maxMailboxesPerEmail": nil, "maxMailboxDepth": 10, "maxSizeMailboxName": 255,
			"maxSizeAttachmentsPerEmail": 50000000, "emailQuerySortOptions": []string{"receivedAt"},
			"mayCreateTopLevelMailbox": true,
		}
	case Submission:
		return map[string]any{"maxDelayedSend": 0, "submissionExtensions": map[string]any{}}
	}
	return map[string]any{}
}

// accountFor 返回用户能看到的账号, 看不到时为 nil。调用方持有 s.mu。
func (s *Server) accountForLocked(u *User, id string) *Account {
	acc := s.accounts[id]
	if acc == nil {
		return nil
	}
	if _, ok := u.sees(acc); !ok {
		return nil
	}
	return acc
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, u *User) {
	accID := strings.TrimPrefix(r.URL.Path, "/jmap/upload/")
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.accountForLocked(u, accID) == nil {
		s.mu.Unlock()
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	s.seq++
	id := fmt.Sprintf("blob%d", s.seq)
	s.blobs[accID+"/"+id] = data
	s.mu.Unlock()
	writeJSON(w, map[string]any{"accountId": accID, "blobId": id, "type": r.Header.Get("Content-Type"), "size": len(data)})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, u *User) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/jmap/download/"), "/", 3)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	acc := s.accountForLocked(u, parts[0])
	var data []byte
	ok := false
	if acc != nil {
		data, ok = s.blobs[parts[0]+"/"+parts[1]]
		if !ok {
			data, ok = acc.rawMessageLocked(parts[1])
		}
	}
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if t := r.URL.Query().Get("type"); t != "" {
		w.Header().Set("Content-Type", t)
	}
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
