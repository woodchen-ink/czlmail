package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/woodchen-ink/czlmail/desktop/internal/htmltext"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 写信工具。create_draft 只存进草稿箱, 由用户在 CZL Mail 里检查后发送, 不需要开关;
// send_email 直接发出, 需开「允许 AI 发送邮件」。两者共用 mcpCompose 组信:
// 回复带 In-Reply-To/References 挂进原会话, 转发带上原附件, 签名与引用的格式和写信面板一致,
// 草稿在写信面板里打开时能正确拆出正文、签名与引用。

type composeIn struct {
	AccountID  string   `json:"accountId,omitempty" jsonschema:"发件账号, 省略时为个人账号; 回复/转发时应与原邮件所在账号一致"`
	Mode       string   `json:"mode,omitempty" jsonschema:"new(默认)、reply(回复发件人)、replyAll(回复全部)、forward(转发)"`
	EmailID    string   `json:"emailId,omitempty" jsonschema:"reply/replyAll/forward 时的原邮件 id"`
	To         []string `json:"to,omitempty" jsonschema:"收件人, 如 a@x.com 或 张三 <a@x.com>; 回复时省略则自动填原发件人"`
	CC         []string `json:"cc,omitempty" jsonschema:"抄送; replyAll 时省略则自动填原收件人与抄送"`
	BCC        []string `json:"bcc,omitempty"`
	Subject    string   `json:"subject,omitempty" jsonschema:"主题; 回复/转发时省略则用 Re:/Fwd: 加原主题"`
	Body       string   `json:"body" jsonschema:"纯文本正文(不含签名与引用, 会自动附上); 空行分段"`
	IdentityID string   `json:"identityId,omitempty" jsonschema:"发件身份 id 或地址, 省略时用默认身份; 见 list_identities"`
}

func (a *App) addComposeTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "create_draft",
		Description: "写一封邮件存进草稿箱(新邮件、回复、回复全部或转发), 不会发出。" +
			"用户在 CZL Mail 的草稿箱里打开检查后自己发送。自动附上签名, 回复与转发自动引用原文。",
		Annotations: writeTool(false, false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in composeIn) (*mcp.CallToolResult, any, error) {
		req, err := a.mcpCompose(in)
		if err != nil {
			return nil, nil, err
		}
		id, err := a.SaveDraft(req)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"draftId": id, "accountId": req.AccountID, "to": req.To, "cc": req.CC, "subject": req.Subject,
			"note": "saved to Drafts; the user can open it in CZL Mail to review and send",
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "send_email",
		Description: "直接发出一封邮件(新邮件、回复、回复全部或转发)。需要用户在 CZL Mail 里开启「允许 AI 发送邮件」; " +
			"发送前必须向用户确认收件人与内容。用户没有明确要求直接发时, 用 create_draft。",
		Annotations: outwardTool(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in composeIn) (*mcp.CallToolResult, any, error) {
		if err := a.mcpRequire(store.SettingMCPAllowSend); err != nil {
			return nil, nil, err
		}
		req, err := a.mcpCompose(in)
		if err != nil {
			return nil, nil, err
		}
		if len(req.To)+len(req.CC)+len(req.BCC) == 0 {
			return nil, nil, errors.New("at least one recipient is required")
		}
		return nil, map[string]any{"sent": true, "to": req.To, "cc": req.CC, "subject": req.Subject}, a.SendEmail(req)
	})
}

// mcpCompose 按模式组出 ComposeRequest。
func (a *App) mcpCompose(in composeIn) (ComposeRequest, error) {
	acc, err := a.mcpAccount(in.AccountID)
	if err != nil {
		return ComposeRequest{}, err
	}
	identity, err := a.pickIdentity(acc, in.IdentityID)
	if err != nil {
		return ComposeRequest{}, err
	}
	req := ComposeRequest{
		AccountID: acc, IdentityID: identity.ID,
		To: parseAddressList(in.To), CC: parseAddressList(in.CC), BCC: parseAddressList(in.BCC),
		Subject: strings.TrimSpace(in.Subject),
	}

	var orig *store.EmailDetail
	quoteText, quoteHTML := "", ""
	switch in.Mode {
	case "", "new":
	case "reply", "replyAll", "forward":
		if in.EmailID == "" {
			return ComposeRequest{}, fmt.Errorf("emailId is required for mode %q", in.Mode)
		}
		if orig, err = a.mcpEmail(acc, in.EmailID); err != nil {
			return ComposeRequest{}, err
		}
	default:
		return ComposeRequest{}, fmt.Errorf("unknown mode %q", in.Mode)
	}

	if orig != nil {
		original := emailText(orig)
		when := orig.ReceivedAt.Local().Format("2006-01-02 15:04")
		if in.Mode == "forward" {
			if req.Subject == "" {
				req.Subject = withSubjectPrefix("Fwd:", orig.Subject)
			}
			// 与界面的普通转发相同: 引用服务器上已有的附件 blob, 不需要重新上传。
			for _, att := range visibleAttachments(orig) {
				req.Attachments = append(req.Attachments, AttachmentRef{BlobID: att.BlobID, Type: att.Type, Name: att.Name})
			}
			header := []string{
				"-------- 转发的邮件 --------",
				"发件人：" + addressLine(orig.From),
				"时间：" + when,
				"收件人：" + addressLine(orig.To),
				"主题：" + orig.Subject,
			}
			quoteText = strings.Join(header, "\n") + "\n\n" + original
			quoteHTML = `<div class="czl-quote"><p>` + joinEscaped(header, "<br>") + `</p>` + textToHTML(original) + `</div>`
		} else {
			if len(req.To) == 0 {
				req.To = orig.From
				if len(orig.ReplyTo) > 0 {
					req.To = orig.ReplyTo
				}
			}
			if in.Mode == "replyAll" && len(req.CC) == 0 {
				req.CC = a.replyAllCC(acc, orig, req.To)
			}
			if req.Subject == "" {
				req.Subject = withSubjectPrefix("Re:", orig.Subject)
			}
			// References 应是原邮件的 References 加它自己的 Message-ID; 本地没存完整链,
			// 与界面相同用 In-Reply-To + Message-ID 近似。
			req.InReplyTo = orig.MessageID
			for _, r := range []string{orig.InReplyTo, orig.MessageID} {
				if r != "" {
					req.References = append(req.References, r)
				}
			}
			intro := fmt.Sprintf("在 %s，%s 写道：", when, addressLine(orig.From))
			quoteText = intro + "\n" + quoteLines(original)
			quoteHTML = `<div class="czl-quote"><p>` + html.EscapeString(intro) +
				`</p><blockquote style="margin:0 0 0 .8ex;border-left:1px solid #ccc;padding-left:1ex">` +
				textToHTML(original) + `</blockquote></div>`
		}
	}

	// 正文 + 签名 + 引用, 与写信面板拼接的结构相同(splitDraftHtml 据此拆回)。
	body := strings.TrimSpace(in.Body)
	textParts, htmlParts := []string{body}, []string{textToHTML(body)}
	if sigText, sigHTML := signatureOf(identity); sigText != "" {
		sep := true
		if s, err := a.GetSettings(); err == nil {
			sep = s.SignatureSeparator
		}
		if sep {
			textParts = append(textParts, "-- \n"+sigText)
			htmlParts = append(htmlParts, `<p>-- </p><div class="signature">`+sigHTML+`</div>`)
		} else {
			textParts = append(textParts, sigText)
			htmlParts = append(htmlParts, `<div class="signature">`+sigHTML+`</div>`)
		}
	}
	if quoteText != "" {
		textParts = append(textParts, quoteText)
		htmlParts = append(htmlParts, quoteHTML)
	}
	req.TextBody = strings.Join(textParts, "\n\n")
	req.HTMLBody = strings.Join(htmlParts, "")
	return req, nil
}

// pickIdentity 选发件身份: 指定的 id 或地址 > 设置里的默认身份 > 与登录地址相同的身份 > 第一个。
// 与写信面板的默认规则一致。发信必须带身份, 服务器不会按账号补 From 头。
func (a *App) pickIdentity(accountID, want string) (Identity, error) {
	ids, err := a.ListIdentities(accountID)
	if err != nil {
		return Identity{}, err
	}
	if len(ids) == 0 {
		return Identity{}, errors.New("this account has no sending identity")
	}
	if want != "" {
		for _, id := range ids {
			if id.ID == want || strings.EqualFold(id.Email, want) {
				return id, nil
			}
		}
		return Identity{}, fmt.Errorf("identity %q not found; see list_identities", want)
	}
	if s, err := a.GetSettings(); err == nil {
		if pref := s.DefaultIdentities[accountID]; pref != "" {
			for _, id := range ids {
				if id.ID == pref {
					return id, nil
				}
			}
		}
	}
	login := strings.ToLower(a.GetSessionStatus().Username)
	for _, id := range ids {
		if strings.ToLower(id.Email) == login {
			return id, nil
		}
	}
	return ids[0], nil
}

// replyAllCC 是回复全部的抄送: 原收件人与抄送, 去掉自己的全部身份与已在收件人里的地址。
func (a *App) replyAllCC(accountID string, orig *store.EmailDetail, to []store.Address) []store.Address {
	skip := map[string]bool{}
	for _, addr := range to {
		skip[strings.ToLower(addr.Email)] = true
	}
	if ids, err := a.ListIdentities(accountID); err == nil {
		for _, id := range ids {
			skip[strings.ToLower(id.Email)] = true
		}
	}
	var out []store.Address
	for _, addr := range append(append([]store.Address{}, orig.To...), orig.CC...) {
		key := strings.ToLower(addr.Email)
		if key == "" || skip[key] {
			continue
		}
		skip[key] = true
		out = append(out, addr)
	}
	return out
}

// signatureOf 返回身份签名的纯文本与 HTML 形式; 只有一种时由另一种推出。
func signatureOf(id Identity) (text, htmlSig string) {
	text = strings.TrimSpace(id.TextSignature)
	htmlSig = strings.TrimSpace(id.HTMLSignature)
	switch {
	case text == "" && htmlSig == "":
		return "", ""
	case text == "":
		text = htmltext.Convert(htmlSig)
	case htmlSig == "":
		htmlSig = textToHTML(text)
	}
	return text, htmlSig
}

var subjectPrefix = map[string]*regexp.Regexp{
	"Re:":  regexp.MustCompile(`(?i)^re\s*:`),
	"Fwd:": regexp.MustCompile(`(?i)^(fwd?|转发)\s*[:：]`),
}

// withSubjectPrefix 已经带前缀的主题不再叠加, 否则长回复链会变成 Re: Re: Re:。
func withSubjectPrefix(prefix, subject string) string {
	if subjectPrefix[prefix].MatchString(strings.TrimSpace(subject)) {
		return subject
	}
	return prefix + " " + subject
}

var paragraphBreak = regexp.MustCompile(`
{2,}`)

// textToHTML 与前端 lib/html.ts 的 textToHtml 相同: 空行分段, 段内换行为 <br>。
func textToHTML(text string) string {
	var b strings.Builder
	for _, p := range regexp.MustCompile(`\n{2,}`).Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(html.EscapeString(p), "\n", "<br>"))
		b.WriteString("</p>")
	}
	return b.String()
}

func joinEscaped(lines []string, sep string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = html.EscapeString(l)
	}
	return strings.Join(out, sep)
}

func quoteLines(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(text, "\n"), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

func addressLine(list []store.Address) string {
	parts := make([]string, 0, len(list))
	for _, addr := range list {
		if addr.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", addr.Name, addr.Email))
		} else {
			parts = append(parts, addr.Email)
		}
	}
	return strings.Join(parts, ", ")
}
