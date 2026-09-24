package syncer

import (
	"context"
	"mime"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 阅读栏「显示详情」: 发件认证、垃圾评分、投递地址等只在邮件头里的信息。
// 不落库 —— 只在用户展开详情时现取一次, 缓存它得给每封邮件多存几 KB 邮件头。

// AuthResult 是 Authentication-Results 里的一项(RFC 8601)。
type AuthResult struct {
	// Method 是 spf / dkim / dmarc / iprev / arc 等, 小写。
	Method string `json:"method"`
	// Result 是 pass / fail / softfail / neutral / none / temperror / permerror 等, 小写。
	Result string `json:"result"`
	// Detail 是界面上跟在结果后面的说明: SPF 的发信地址、DKIM 的签名域、iprev 的 IP。
	Detail string `json:"detail"`
	// Policy 只有 DMARC 有: 发件域公布的策略(none / quarantine / reject)。
	Policy string `json:"policy"`
}

// HeaderInfo 是邮件详情面板的数据。
type HeaderInfo struct {
	SentAt     *time.Time      `json:"sentAt"`
	Sender     []store.Address `json:"sender"`
	References []string        `json:"references"`
	// DeliveredTo 是投递时的实际收件地址(Delivered-To / X-Original-To):
	// 密送与邮件列表转来的邮件, 收件人一栏里没有自己的地址, 只有这里看得出发到了哪个邮箱。
	DeliveredTo []string `json:"deliveredTo"`
	ContentType string   `json:"contentType"`
	// AuthServer 是写 Authentication-Results 的服务器(authserv-id)。
	AuthServer string       `json:"authServer"`
	Auth       []AuthResult `json:"auth"`
	// SpamVerdict 为 spam / ham, 空表示邮件头里没有垃圾评分。
	SpamVerdict string `json:"spamVerdict"`
	SpamScore   string `json:"spamScore"`
}

// FetchHeaderInfo 从服务器取邮件头并解析成详情面板的数据。
func (s *Syncer) FetchHeaderInfo(ctx context.Context, accountID, emailID string) (HeaderInfo, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Get{
		Account:    jmap.ID(accountID),
		IDs:        []jmap.ID{jmap.ID(emailID)},
		Properties: []string{"id", "headers", "sentAt", "sender", "references"},
	})
	resp, err := s.do(req)
	if err != nil {
		return HeaderInfo{}, err
	}
	got, ok := resp.Responses[0].Args.(*email.GetResponse)
	if !ok || len(got.List) == 0 {
		return HeaderInfo{}, &Error{Code: CodeUnhandled, Msg: "email not found on server"}
	}
	e := got.List[0]
	info := ParseHeaderInfo(e.Headers)
	info.SentAt = e.SentAt
	info.References = e.References
	for _, a := range e.Sender {
		if a != nil {
			info.Sender = append(info.Sender, store.Address{Name: a.Name, Email: a.Email})
		}
	}
	return info, nil
}

// ParseHeaderInfo 解析认证结果、垃圾评分、投递地址与正文类型。
//
// 邮件头按从上到下排列, 最上面的是最后一跳(自己的服务器)加的。认证结果与垃圾评分
// 只取第一条: 下面的可能是发件方自己写的, 伪造起来毫无成本。
func ParseHeaderInfo(headers []*email.Header) HeaderInfo {
	var info HeaderInfo
	var haveAuth, haveSpamStatus bool
	var receivedSPF string
	var spamFlag string
	for _, h := range headers {
		if h == nil {
			continue
		}
		v := unfold(h.Value)
		switch strings.ToLower(h.Name) {
		case "authentication-results":
			if !haveAuth {
				haveAuth = true
				info.AuthServer, info.Auth = parseAuthResults(v)
			}
		case "received-spf":
			if receivedSPF == "" {
				receivedSPF = v
			}
		case "x-spam-status":
			if !haveSpamStatus {
				haveSpamStatus = true
				info.SpamVerdict, info.SpamScore = parseSpamStatus(v)
			}
		case "x-spam-score":
			if info.SpamScore == "" {
				info.SpamScore = strings.TrimSpace(v)
			}
		case "x-spam-flag":
			if spamFlag == "" {
				spamFlag = strings.ToLower(strings.TrimSpace(v))
			}
		case "delivered-to", "x-original-to":
			addr := strings.TrimSpace(receiptAddress(v))
			if addr != "" && !containsFold(info.DeliveredTo, addr) {
				info.DeliveredTo = append(info.DeliveredTo, addr)
			}
		case "content-type":
			if info.ContentType == "" {
				if mt, _, err := mime.ParseMediaType(v); err == nil {
					info.ContentType = mt
				} else {
					info.ContentType = strings.ToLower(strings.TrimSpace(strings.Split(v, ";")[0]))
				}
			}
		}
	}
	if info.SpamVerdict == "" {
		switch spamFlag {
		case "yes":
			info.SpamVerdict = "spam"
		case "no":
			info.SpamVerdict = "ham"
		}
	}
	if info.SpamVerdict == "" && info.SpamScore != "" {
		info.SpamVerdict = "ham"
	}
	// 没有 Authentication-Results 或其中缺 SPF 时, 退回 Received-SPF。
	if receivedSPF != "" && !hasMethod(info.Auth, "spf") {
		if r := parseReceivedSPF(receivedSPF); r.Result != "" {
			info.Auth = append([]AuthResult{r}, info.Auth...)
		}
	}
	return info
}

// parseAuthResults 解析 "authserv-id; method=result (comment) ptype.prop=value; ..."。
func parseAuthResults(v string) (string, []AuthResult) {
	parts := splitOutside(v, ';')
	if len(parts) == 0 {
		return "", nil
	}
	server := strings.Fields(stripComments(parts[0], nil))
	serverID := ""
	if len(server) > 0 {
		serverID = server[0]
	}
	var out []AuthResult
	for _, part := range parts[1:] {
		var comments []string
		fields := strings.Fields(stripComments(part, &comments))
		if len(fields) == 0 {
			continue
		}
		method, result, ok := strings.Cut(fields[0], "=")
		if !ok || strings.EqualFold(method, "none") {
			continue
		}
		r := AuthResult{Method: strings.ToLower(method), Result: strings.ToLower(result)}
		props := map[string]string{}
		for _, f := range fields[1:] {
			k, val, ok := strings.Cut(f, "=")
			if ok {
				props[strings.ToLower(k)] = strings.Trim(val, `"`)
			}
		}
		switch r.Method {
		case "spf":
			r.Detail = first(props["smtp.mailfrom"], props["smtp.helo"])
		case "dkim", "domainkeys":
			r.Detail = first(props["header.d"], props["header.i"])
		case "dmarc":
			r.Detail = props["header.from"]
			r.Policy = props["policy.dmarc"]
			if r.Policy == "" {
				r.Policy = commentValue(comments, "p")
			}
		case "iprev":
			r.Detail = first(props["policy.iprev"], props["smtp.remote-ip"])
		case "arc":
			r.Detail = props["header.oldest-pass"]
		}
		out = append(out, r)
	}
	return serverID, out
}

// parseReceivedSPF 解析 "pass (comment) key=value; ..."(RFC 7208 §9.1)。
func parseReceivedSPF(v string) AuthResult {
	fields := strings.Fields(stripComments(v, nil))
	if len(fields) == 0 {
		return AuthResult{}
	}
	r := AuthResult{Method: "spf", Result: strings.ToLower(fields[0])}
	for _, f := range strings.FieldsFunc(v, func(c rune) bool { return c == ';' || c == ' ' }) {
		if k, val, ok := strings.Cut(f, "="); ok && strings.EqualFold(k, "envelope-from") {
			r.Detail = strings.Trim(val, `"<>`)
		}
	}
	return r
}

// parseSpamStatus 解析 "Yes/No, score=-2.9 required=5.0 ..."(SpamAssassin / Stalwart)。
func parseSpamStatus(v string) (verdict, score string) {
	head, rest, _ := strings.Cut(v, ",")
	switch strings.ToLower(strings.TrimSpace(head)) {
	case "yes":
		verdict = "spam"
	case "no":
		verdict = "ham"
	}
	for _, f := range strings.Fields(rest) {
		if k, val, ok := strings.Cut(f, "="); ok && strings.EqualFold(k, "score") {
			score = strings.TrimRight(val, ",")
			// "-2.90" 显示成 "-2.9"; 解析不了就原样保留。
			if n, err := strconv.ParseFloat(score, 64); err == nil {
				score = strconv.FormatFloat(n, 'f', -1, 64)
			}
		}
	}
	return verdict, score
}

// stripComments 去掉 RFC 5322 注释(可嵌套的圆括号), 注释内容收进 comments。
func stripComments(v string, comments *[]string) string {
	var b, c strings.Builder
	depth := 0
	quoted := false
	for _, r := range v {
		switch {
		case r == '"' && depth == 0:
			quoted = !quoted
			b.WriteRune(r)
		case r == '(' && !quoted:
			if depth > 0 {
				c.WriteRune(r)
			}
			depth++
		case r == ')' && !quoted && depth > 0:
			depth--
			if depth == 0 {
				if comments != nil {
					*comments = append(*comments, c.String())
				}
				c.Reset()
				b.WriteRune(' ')
			} else {
				c.WriteRune(r)
			}
		case depth > 0:
			c.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitOutside 按分隔符切分, 引号与注释内的不切。
func splitOutside(v string, sep rune) []string {
	var out []string
	var b strings.Builder
	quoted := false
	depth := 0
	for _, r := range v {
		switch {
		case r == '"' && depth == 0:
			quoted = !quoted
			b.WriteRune(r)
		case r == '(' && !quoted:
			depth++
			b.WriteRune(r)
		case r == ')' && !quoted && depth > 0:
			depth--
			b.WriteRune(r)
		case r == sep && !quoted && depth == 0:
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// commentValue 从注释里取 "key=value"(如 DMARC 的 "(p=none dis=none)")。
func commentValue(comments []string, key string) string {
	for _, c := range comments {
		for _, f := range strings.Fields(c) {
			if k, val, ok := strings.Cut(f, "="); ok && strings.EqualFold(k, key) {
				return strings.ToLower(strings.Trim(val, `";,`))
			}
		}
	}
	return ""
}

func hasMethod(list []AuthResult, method string) bool {
	for _, r := range list {
		if r.Method == method {
			return true
		}
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
