package main

import (
	"fmt"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 邮件的写操作。全部转交 syncer 走服务端 Email/set, 本地不先改 ——
// 服务端接受后会推 StateChange, 增量同步把结果带回来, 只有一份真相。

func (a *App) currentSyncer() (*syncer.Syncer, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sync == nil {
		return nil, errNotSignedIn
	}
	return a.sync, nil
}

// FetchBody 拉取并缓存单封邮件的正文, 返回带正文的完整邮件。
func (a *App) FetchBody(accountID, emailID string) (*store.EmailDetail, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	if err := s.FetchBody(a.ctx, accountID, emailID); err != nil {
		return nil, err
	}

	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Email(a.ctx, accountID, emailID)
}

func (a *App) MarkRead(accountID string, emailIDs []string, read bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.MarkRead(a.ctx, accountID, emailIDs, read)
}

func (a *App) MarkFlagged(accountID string, emailIDs []string, flagged bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.MarkFlagged(a.ctx, accountID, emailIDs, flagged)
}

func (a *App) MoveEmails(accountID string, emailIDs []string, targetMailboxID string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.Move(a.ctx, accountID, emailIDs, targetMailboxID)
}

// TrashEmails 把邮件移入回收站。
//
// 与 DeleteEmails 分开是刻意的: 界面上的"删除"永远走这里, 只有在回收站里
// 再次删除才调用 DeleteEmails。JMAP 的 destroy 不可撤销, 不该藏在一个
// 用户以为可以后悔的按钮后面。
func (a *App) TrashEmails(accountID string, emailIDs []string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}

	trash, err := mailboxByRole(st, a.ctx, accountID, "trash")
	if err != nil {
		return err
	}
	if trash == "" {
		// 没有回收站的账号(某些共享邮箱)只能直接销毁, 由界面明确告知用户。
		return errNoTrashMailbox
	}
	return a.MoveEmails(accountID, emailIDs, trash)
}

// DeleteEmails 彻底销毁邮件, 不可撤销。
func (a *App) DeleteEmails(accountID string, emailIDs []string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.Delete(a.ctx, accountID, emailIDs)
}

// Identity 是可用的发件身份。
type Identity struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	TextSignature string `json:"textSignature"`
	HTMLSignature string `json:"htmlSignature"`
}

func (a *App) ListIdentities(accountID string) ([]Identity, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}

	list, err := s.Identities(a.ctx, accountID)
	if err != nil {
		return nil, err
	}

	out := make([]Identity, 0, len(list))
	for _, id := range list {
		if id == nil {
			continue
		}
		out = append(out, Identity{
			ID:            string(id.ID),
			Name:          id.Name,
			Email:         id.Email,
			TextSignature: id.TextSignature,
			HTMLSignature: id.HTMLSignature,
		})
	}
	return out, nil
}

// ComposeRequest 是前端提交的一封待发送邮件。
type ComposeRequest struct {
	AccountID  string          `json:"accountId"`
	IdentityID string          `json:"identityId"`
	To         []store.Address `json:"to"`
	CC         []store.Address `json:"cc"`
	BCC        []store.Address `json:"bcc"`
	Subject    string          `json:"subject"`
	TextBody   string          `json:"textBody"`
	HTMLBody   string          `json:"htmlBody"`
	InReplyTo  string          `json:"inReplyTo"`
	References []string        `json:"references"`
	// Attachments 引用已在服务器上的 blob, 用于作为附件转发。
	Attachments []AttachmentRef `json:"attachments"`
	// DraftID 是正在编辑的草稿; 发送或再次保存成功后删除旧版本。
	DraftID            string `json:"draftId"`
	RequestReadReceipt bool   `json:"requestReadReceipt"`
	// SendAt 为 RFC 3339 时间时定时发送, 空表示立即发送。
	SendAt string `json:"sendAt"`
	// SendSeparately 为真时给每个收件人单独发一封, 收件人之间互相看不到。
	SendSeparately bool `json:"sendSeparately"`
}

// AttachmentRef 引用服务器上已存在的 blob。
type AttachmentRef struct {
	BlobID string `json:"blobId"`
	Type   string `json:"type"`
	Name   string `json:"name"`
}

// SendEmail 发送一封邮件。
func (a *App) SendEmail(req ComposeRequest) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}

	drafts, err := mailboxByRole(st, a.ctx, req.AccountID, "drafts")
	if err != nil {
		return err
	}
	sent, err := mailboxByRole(st, a.ctx, req.AccountID, "sent")
	if err != nil {
		return err
	}

	d, err := a.draftFrom(req)
	if err != nil {
		return err
	}
	if !req.SendSeparately {
		return s.Send(a.ctx, d, drafts, sent)
	}

	// 分开发送: 抄送与密送也各自成为单独一封的收件人, 否则他们仍会看到彼此。
	var recipients []store.Address
	for _, list := range [][]store.Address{req.To, req.CC, req.BCC} {
		recipients = append(recipients, list...)
	}
	if len(recipients) == 0 {
		return fmt.Errorf("1121 no recipients")
	}
	if len(recipients) > maxSeparateRecipients {
		return fmt.Errorf("2091 at most %d recipients can be sent separately", maxSeparateRecipients)
	}
	for i, r := range recipients {
		one := d
		one.To, one.CC, one.BCC = []store.Address{r}, nil, nil
		// 只有最后一封成功后才删除原草稿, 中途失败时草稿还在。
		if i < len(recipients)-1 {
			one.ReplaceDraftID = ""
		}
		if err := s.Send(a.ctx, one, drafts, sent); err != nil {
			return fmt.Errorf("2092 sent %d of %d, failed at %s: %w", i, len(recipients), r.Email, err)
		}
	}
	return nil
}

// maxSeparateRecipients 限制分开发送的人数: 每封都是一次独立提交, 太多会触发服务器的发信频率限制。
const maxSeparateRecipients = 100

// SaveDraft 保存草稿, 返回新的草稿 id(每次保存都会换 id)。
func (a *App) SaveDraft(req ComposeRequest) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	drafts, err := mailboxByRole(st, a.ctx, req.AccountID, "drafts")
	if err != nil {
		return "", err
	}
	d, err := a.draftFrom(req)
	if err != nil {
		return "", err
	}
	d.SendAt = time.Time{}
	return s.SaveDraft(a.ctx, d, drafts)
}

// DiscardDraft 删除草稿。
func (a *App) DiscardDraft(accountID, draftID string) error {
	if draftID == "" {
		return nil
	}
	return a.DeleteEmails(accountID, []string{draftID})
}

// MaxDelayedSend 返回账号允许的最长定时发送秒数, 0 表示不支持定时发送。
func (a *App) MaxDelayedSend(accountID string) int {
	s, err := a.currentSyncer()
	if err != nil {
		return 0
	}
	return s.MaxDelayedSend(accountID)
}

func (a *App) draftFrom(req ComposeRequest) (syncer.Draft, error) {
	d := syncer.Draft{
		AccountID:          req.AccountID,
		IdentityID:         req.IdentityID,
		To:                 req.To,
		CC:                 req.CC,
		BCC:                req.BCC,
		Subject:            req.Subject,
		TextBody:           req.TextBody,
		HTMLBody:           req.HTMLBody,
		InReplyTo:          req.InReplyTo,
		References:         req.References,
		Attachments:        toSyncerAttachments(req.Attachments),
		RequestReadReceipt: req.RequestReadReceipt,
		ReplaceDraftID:     req.DraftID,
	}
	if ids, err := a.ListIdentities(req.AccountID); err == nil {
		for _, id := range ids {
			if id.ID == req.IdentityID {
				d.FromEmail = id.Email
				d.FromName = id.Name
			}
		}
	}
	if req.SendAt != "" {
		t, err := time.Parse(time.RFC3339, req.SendAt)
		if err != nil {
			return d, fmt.Errorf("2090 invalid scheduled time")
		}
		d.SendAt = t
	}
	return d, nil
}

func toSyncerAttachments(in []AttachmentRef) []syncer.Attachment {
	out := make([]syncer.Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, syncer.Attachment{BlobID: a.BlobID, Type: a.Type, Name: a.Name})
	}
	return out
}

// UpdateSignature 修改发件身份的签名。
func (a *App) UpdateSignature(accountID, identityID, textSignature string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	// 只改纯文本时保留 HTML 签名会出现两份不一致的签名; 由调用方决定两者。
	return s.UpdateSignature(a.ctx, accountID, identityID, textSignature, "")
}

// UpdateSignatureHTML 同时保存 HTML 签名与由它生成的纯文本签名。
func (a *App) UpdateSignatureHTML(accountID, identityID, html, text string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.UpdateSignature(a.ctx, accountID, identityID, text, html)
}

// ScheduledItem 是定时邮件列表的一行。
type ScheduledItem struct {
	SubmissionID string    `json:"submissionId"`
	EmailID      string    `json:"emailId"`
	SendAt       time.Time `json:"sendAt"`
	Subject      string    `json:"subject"`
}

// ListScheduledSends 列出等待发出的定时邮件。
func (a *App) ListScheduledSends(accountID string) ([]ScheduledItem, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	list, err := s.ScheduledSends(a.ctx, accountID)
	if err != nil {
		return nil, err
	}
	st, _ := a.currentStore()
	out := make([]ScheduledItem, 0, len(list))
	for _, it := range list {
		item := ScheduledItem{SubmissionID: it.SubmissionID, EmailID: it.EmailID, SendAt: it.SendAt}
		if st != nil {
			if e, err := st.Email(a.ctx, accountID, it.EmailID); err == nil {
				item.Subject = e.Subject
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// CancelScheduledSend 取消定时发送, 邮件回到草稿箱。
func (a *App) CancelScheduledSend(accountID, submissionID, emailID string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	drafts, _ := mailboxByRole(st, a.ctx, accountID, "drafts")
	return s.CancelScheduledSend(a.ctx, accountID, submissionID, emailID, drafts)
}
