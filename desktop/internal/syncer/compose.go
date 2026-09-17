package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"git.sr.ht/~rockorager/go-jmap/mail/emailsubmission"
	"git.sr.ht/~rockorager/go-jmap/mail/identity"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// Draft 是一封待发送或待保存的邮件。
type Draft struct {
	AccountID  string
	IdentityID string

	To      []store.Address
	CC      []store.Address
	BCC     []store.Address
	Subject string
	// TextBody 与 HTMLBody 至少要有一个。两者都给时构成 multipart/alternative,
	// 收件人的客户端自行选择渲染哪个。
	TextBody string
	HTMLBody string

	// InReplyTo 与 References 用于把回复挂进原线程。
	// 缺了它们, 回复在对方客户端里会显示成一封孤立的新邮件。
	InReplyTo  string
	References []string

	// Attachments 引用服务器上已存在的 blob。作为附件转发时就是原邮件的 blob,
	// 不需要下载再上传一遍。
	Attachments []Attachment

	// FromEmail / FromName 来自所选发件身份, 写进邮件的 From 头。
	// 服务器不会按提交时的 identityId 补 From, 漏了它对方看到的就是"无发件人"。
	FromEmail string
	FromName  string
	// RequestReadReceipt 请求已读回执(Disposition-Notification-To)。
	RequestReadReceipt bool
	// SendAt 非零时定时发送(SMTP FUTURERELEASE 的 HOLDFOR), 不能晚于服务器允许的最大延迟。
	SendAt time.Time
	// ReplaceDraftID 是被这次发送/保存取代的旧草稿, 成功后删除。
	ReplaceDraftID string
}

// Attachment 是对一个已上传 blob 的引用。
type Attachment struct {
	BlobID string
	Type   string
	Name   string
}

// Identities 列出该账号可用的发件身份。
func (s *Syncer) Identities(ctx context.Context, accountID string) ([]*identity.Identity, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&identity.Get{Account: jmap.ID(accountID)})

	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}

	got, ok := resp.Responses[0].Args.(*identity.GetResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to Identity/get"}
	}
	return got.List, nil
}

// Send 创建邮件并提交发送。
//
// 全程一个请求完成: Email/set 建草稿, EmailSubmission/set 用 back-reference
// 引用刚创建的邮件 id 提交, 同一个调用里再把邮件从草稿箱移到已发送。
// 拆成多次请求会留下中间态 —— 建好了但没发出去的草稿, 或发出去了却还躺在草稿箱里的邮件。
func (s *Syncer) Send(ctx context.Context, d Draft, draftMailboxID, sentMailboxID string) error {
	if d.IdentityID == "" {
		return fmt.Errorf("1120 no sending identity selected")
	}
	if d.FromEmail == "" {
		return fmt.Errorf("1126 sending identity not found")
	}
	if len(d.To) == 0 && len(d.CC) == 0 && len(d.BCC) == 0 {
		return fmt.Errorf("1121 no recipients")
	}
	if draftMailboxID == "" {
		return fmt.Errorf("1122 drafts mailbox not found")
	}

	msg := buildEmail(d, draftMailboxID)

	var envelope *emailsubmission.Envelope
	if !d.SendAt.IsZero() {
		hold := int(time.Until(d.SendAt).Seconds())
		if hold <= 0 {
			return fmt.Errorf("1123 scheduled time must be in the future")
		}
		if max := s.MaxDelayedSend(d.AccountID); max <= 0 || hold > max {
			return fmt.Errorf("1124 scheduled time is later than the server allows")
		}
		if d.FromEmail == "" {
			return fmt.Errorf("1125 sender address is required for scheduled send")
		}
		envelope = &emailsubmission.Envelope{
			MailFrom: &emailsubmission.Address{Email: d.FromEmail, Parameters: map[string]string{"HOLDFOR": fmt.Sprint(hold)}},
		}
		for _, list := range [][]store.Address{d.To, d.CC, d.BCC} {
			for _, a := range list {
				envelope.RcptTo = append(envelope.RcptTo, &emailsubmission.Address{Email: a.Email})
			}
		}
	}

	const createID = jmap.ID("draft")
	req := &jmap.Request{Context: ctx}

	createArgs, err := emailCreate(d, msg)
	if err != nil {
		return err
	}
	createCall := req.Invoke(createArgs)

	// onSuccessUpdateEmail 让服务端在提交成功后原子地改邮件:
	// 去掉 $draft 标志, 从草稿箱移到已发送。
	submission := &emailsubmission.Set{
		Account: jmap.ID(d.AccountID),
		Create: map[jmap.ID]*emailsubmission.EmailSubmission{
			"sub": {
				IdentityID: jmap.ID(d.IdentityID),
				EmailID:    "#" + createID,
				Envelope:   envelope,
			},
		},
		OnSuccessUpdateEmail: map[jmap.ID]jmap.Patch{
			"#sub": onSendPatch(draftMailboxID, sentMailboxID),
		},
	}
	req.Invoke(submission)

	resp, err := s.do(req)
	if err != nil {
		return err
	}

	if len(resp.Responses) < 2 {
		return &Error{Code: CodeUnhandled, Msg: "incomplete response to send request"}
	}
	_ = createCall

	if created, ok := resp.Responses[0].Args.(*email.SetResponse); ok {
		if err := firstSetError(created.NotCreated); err != nil {
			return err
		}
	}
	if sent, ok := resp.Responses[1].Args.(*emailsubmission.SetResponse); ok {
		if err := firstSetError(sent.NotCreated); err != nil {
			return err
		}
	}

	// 旧草稿在发送成功之后再删: 同一个请求里先删, 发送失败时用户唯一的副本也没了。
	if d.ReplaceDraftID != "" {
		if err := s.destroyEmail(ctx, d.AccountID, d.ReplaceDraftID); err != nil {
			s.log.Warn("destroy replaced draft", "err", err)
		}
	}
	return s.SyncAccount(ctx, d.AccountID)
}

// SaveDraft 把邮件存进草稿箱并返回新草稿 id; ReplaceDraftID 指向的旧版本随后删除。
// JMAP 的邮件内容不可修改, 更新草稿只能"建新删旧"。
func (s *Syncer) SaveDraft(ctx context.Context, d Draft, draftMailboxID string) (string, error) {
	if draftMailboxID == "" {
		return "", fmt.Errorf("1122 drafts mailbox not found")
	}
	msg := buildEmail(d, draftMailboxID)
	req := &jmap.Request{Context: ctx}
	createArgs, err := emailCreate(d, msg)
	if err != nil {
		return "", err
	}
	req.Invoke(createArgs)
	resp, err := s.do(req)
	if err != nil {
		return "", err
	}
	set, ok := resp.Responses[0].Args.(*email.SetResponse)
	if !ok {
		return "", &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/set"}
	}
	if err := firstSetError(set.NotCreated); err != nil {
		return "", err
	}
	created := set.Created["draft"]
	if created == nil {
		return "", &Error{Code: CodeUnhandled, Msg: "draft not created"}
	}
	if d.ReplaceDraftID != "" && d.ReplaceDraftID != string(created.ID) {
		if err := s.destroyEmail(ctx, d.AccountID, d.ReplaceDraftID); err != nil {
			s.log.Warn("destroy replaced draft", "err", err)
		}
	}
	return string(created.ID), s.SyncAccount(ctx, d.AccountID)
}

// emailCreate 构造 Email/set 的创建调用。go-jmap 的 Email 结构体表达不了
// "header:Xxx:asText" 这类按名写入的头(Stalwart 拒绝 headers 数组), 这里转成 map 再补字段。
func emailCreate(d Draft, msg *email.Email) (*jmapx.Call, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	obj := map[string]any{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if d.RequestReadReceipt && d.FromEmail != "" {
		obj["header:Disposition-Notification-To:asText"] = d.FromEmail
	}
	return &jmapx.Call{Method: "Email/set", Args: map[string]any{
		"accountId": d.AccountID,
		"create":    map[string]any{"draft": obj},
	}}, nil
}

func (s *Syncer) destroyEmail(ctx context.Context, accountID, id string) error {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Set{Account: jmap.ID(accountID), Destroy: []jmap.ID{jmap.ID(id)}})
	_, err := s.do(req)
	return err
}

// MaxDelayedSend 返回服务器允许的最长定时发送秒数, 不支持时为 0。
func (s *Syncer) MaxDelayedSend(accountID string) int {
	if s.client.Session == nil {
		return 0
	}
	acc, ok := s.client.Session.Accounts[jmap.ID(accountID)]
	if !ok {
		return 0
	}
	var cap struct {
		MaxDelayedSend       int                 `json:"maxDelayedSend"`
		SubmissionExtensions map[string][]string `json:"submissionExtensions"`
	}
	raw, ok := acc.RawCapabilities["urn:ietf:params:jmap:submission"]
	if !ok || json.Unmarshal(raw, &cap) != nil {
		return 0
	}
	if _, ok := cap.SubmissionExtensions["FUTURERELEASE"]; !ok {
		return 0
	}
	return cap.MaxDelayedSend
}

// onSendPatch 构造发送成功后对邮件的修改。
func onSendPatch(draftMailboxID, sentMailboxID string) jmap.Patch {
	patch := jmap.Patch{
		"keywords/$draft":              nil,
		"mailboxIds/" + draftMailboxID: nil,
	}
	// 服务器没有已发送邮箱时只去掉草稿归属, 不硬造一个。
	if sentMailboxID != "" {
		patch["mailboxIds/"+sentMailboxID] = true
	}
	return patch
}

func buildEmail(d Draft, draftMailboxID string) *email.Email {
	now := time.Now()
	msg := &email.Email{
		MailboxIDs: map[jmap.ID]bool{jmap.ID(draftMailboxID): true},
		Keywords:   map[string]bool{"$draft": true, "$seen": true},
		Subject:    d.Subject,
		From:       toJMAPAddresses(fromAddress(d)),
		To:         toJMAPAddresses(d.To),
		CC:         toJMAPAddresses(d.CC),
		BCC:        toJMAPAddresses(d.BCC),
		SentAt:     &now,
		BodyValues: map[string]*email.BodyValue{},
	}

	if d.InReplyTo != "" {
		msg.InReplyTo = []string{d.InReplyTo}
	}

	if len(d.References) > 0 {
		msg.References = d.References
	}

	// partId 是本次请求内的自定义标识, 把 bodyValues 与 textBody/htmlBody 关联起来。
	if d.TextBody != "" {
		msg.BodyValues["text"] = &email.BodyValue{Value: d.TextBody}
		msg.TextBody = []*email.BodyPart{{PartID: "text", Type: "text/plain"}}
	}
	if d.HTMLBody != "" {
		msg.BodyValues["html"] = &email.BodyValue{Value: d.HTMLBody}
		msg.HTMLBody = []*email.BodyPart{{PartID: "html", Type: "text/html"}}
	}

	for _, a := range d.Attachments {
		if a.BlobID == "" {
			continue
		}
		msg.Attachments = append(msg.Attachments, &email.BodyPart{
			BlobID:      jmap.ID(a.BlobID),
			Type:        a.Type,
			Name:        a.Name,
			Disposition: "attachment",
		})
	}

	return msg
}

func fromAddress(d Draft) []store.Address {
	if d.FromEmail == "" {
		return nil
	}
	return []store.Address{{Name: d.FromName, Email: d.FromEmail}}
}

func toJMAPAddresses(in []store.Address) []*mail.Address {
	if len(in) == 0 {
		return nil
	}
	out := make([]*mail.Address, 0, len(in))
	for _, a := range in {
		out = append(out, &mail.Address{Name: a.Name, Email: a.Email})
	}
	return out
}

// UpdateSignature 修改发件身份的签名。签名存在服务端, 所有客户端共用。
func (s *Syncer) UpdateSignature(ctx context.Context, accountID, identityID, text, html string) error {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&identity.Set{
		Account: jmap.ID(accountID),
		Update: map[jmap.ID]jmap.Patch{
			jmap.ID(identityID): {"textSignature": text, "htmlSignature": html},
		},
	})
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	set, ok := resp.Responses[0].Args.(*identity.SetResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to Identity/set"}
	}
	if e, ok := set.NotUpdated[jmap.ID(identityID)]; ok {
		desc := ""
		if e.Description != nil {
			desc = *e.Description
		}
		return &Error{Code: CodeMethod, Msg: "identity not updated: " + e.Type + " " + desc}
	}
	return nil
}

// ScheduledSend 是尚未发出的定时邮件。
type ScheduledSend struct {
	SubmissionID string    `json:"submissionId"`
	EmailID      string    `json:"emailId"`
	SendAt       time.Time `json:"sendAt"`
}

// ScheduledSends 列出账号里等待发出的定时邮件。
func (s *Syncer) ScheduledSends(ctx context.Context, accountID string) ([]ScheduledSend, error) {
	req := &jmap.Request{Context: ctx}
	q := req.Invoke(&emailsubmission.Query{
		Account: jmap.ID(accountID),
		Filter:  &emailsubmission.FilterCondition{UndoStatus: "pending"},
	})
	req.Invoke(&emailsubmission.Get{
		Account:      jmap.ID(accountID),
		ReferenceIDs: &jmap.ResultReference{ResultOf: q, Name: "EmailSubmission/query", Path: "/ids"},
	})
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	got, ok := resp.Responses[1].Args.(*emailsubmission.GetResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to EmailSubmission/get"}
	}
	var out []ScheduledSend
	for _, sub := range got.List {
		item := ScheduledSend{SubmissionID: string(sub.ID), EmailID: string(sub.EmailID)}
		if sub.SendAt != nil {
			item.SendAt = *sub.SendAt
		}
		out = append(out, item)
	}
	return out, nil
}

// CancelScheduledSend 取消定时发送, 并把邮件放回草稿箱供继续编辑。
func (s *Syncer) CancelScheduledSend(ctx context.Context, accountID, submissionID, emailID, draftMailboxID string) error {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&emailsubmission.Set{
		Account: jmap.ID(accountID),
		Update:  map[jmap.ID]jmap.Patch{jmap.ID(submissionID): {"undoStatus": "canceled"}},
	})
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	if set, ok := resp.Responses[0].Args.(*emailsubmission.SetResponse); ok {
		if e, bad := set.NotUpdated[jmap.ID(submissionID)]; bad {
			return &Error{Code: CodeMethod, Msg: "cannot cancel scheduled send: " + e.Type}
		}
	}
	if emailID != "" && draftMailboxID != "" {
		move := &jmap.Request{Context: ctx}
		move.Invoke(&email.Set{
			Account: jmap.ID(accountID),
			Update: map[jmap.ID]jmap.Patch{jmap.ID(emailID): {
				"mailboxIds":      map[jmap.ID]bool{jmap.ID(draftMailboxID): true},
				"keywords/$draft": true,
			}},
		})
		if _, err := s.do(move); err != nil {
			s.log.Warn("move canceled email to drafts", "err", err)
		}
	}
	return s.SyncAccount(ctx, accountID)
}
