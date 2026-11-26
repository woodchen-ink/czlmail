package syncer

import (
	"context"
	"database/sql"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// emailMetaProperties 是列表视图所需的全部字段, 刻意不含 bodyValues /
// textBody / htmlBody: 正文按封按需拉取, 混进同步会让一次增量动辄几十 MB。
var emailMetaProperties = []string{
	"id", "blobId", "threadId", "mailboxIds", "keywords", "size",
	"receivedAt", "sentAt", "from", "to", "cc", "bcc", "replyTo",
	"subject", "preview", "hasAttachment", "messageId", "inReplyTo",
	// bodyStructure 只取每个部件的类型、处置方式与文件名 (emailBodyProperties): 用来认出 S/MIME 签名邮件,
	// 让签名部件 smime.p7s 不算附件, 并在列表里显示附件名。
	"bodyStructure",
}

// emailBodyProperties 限定 bodyStructure 里每个部件返回的字段, 让结构树只有几十个字节。
var emailBodyProperties = []string{"type", "disposition", "name"}

// errCannotCalculateChanges 是服务端无法基于给定 state 计算增量时返回的类型。
// 通常因为该 state 过旧已被服务端回收, 唯一出路是丢弃本地 state 重新引导。
const errCannotCalculateChanges = "cannotCalculateChanges"

// syncMailboxes 同步邮箱树。邮箱数量以十计, 不做窗口化, 一律全量取回。
func (s *Syncer) syncMailboxes(ctx context.Context, accountID string) error {
	localState, err := s.store.State(ctx, accountID, store.TypeMailbox)
	if err != nil {
		return err
	}

	if localState == "" {
		return s.bootstrapMailboxes(ctx, accountID)
	}

	req := &jmap.Request{Context: ctx}
	req.Invoke(&mailbox.Changes{Account: s.server(accountID), SinceState: localState})

	resp, err := s.do(req)
	if err != nil {
		if methodErrorType(err) == errCannotCalculateChanges {
			s.log.Warn("mailbox state expired, re-bootstrapping", "account", accountID)
			return s.rebootstrap(ctx, accountID, store.TypeMailbox, s.bootstrapMailboxes)
		}
		return err
	}

	changes, ok := resp.Responses[0].Args.(*mailbox.ChangesResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to Mailbox/changes"}
	}

	touched := append(append([]jmap.ID{}, changes.Created...), changes.Updated...)
	var boxes []store.Mailbox
	if len(touched) > 0 {
		if boxes, err = s.fetchMailboxes(ctx, accountID, touched); err != nil {
			return err
		}
	}

	destroyed := idsToStrings(changes.Destroyed)
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertMailboxes(ctx, tx, accountID, boxes); err != nil {
			return err
		}
		if err := store.DeleteMailboxes(ctx, tx, accountID, destroyed); err != nil {
			return err
		}
		return store.SetState(ctx, tx, accountID, store.TypeMailbox, changes.NewState)
	})
}

func (s *Syncer) bootstrapMailboxes(ctx context.Context, accountID string) error {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&mailbox.Get{Account: s.server(accountID)})

	resp, err := s.do(req)
	if err != nil {
		return err
	}

	got, ok := resp.Responses[0].Args.(*mailbox.GetResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to Mailbox/get"}
	}

	boxes := make([]store.Mailbox, 0, len(got.List))
	for _, m := range got.List {
		boxes = append(boxes, toStoreMailbox(m))
	}

	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertMailboxes(ctx, tx, accountID, boxes); err != nil {
			return err
		}
		return store.SetState(ctx, tx, accountID, store.TypeMailbox, got.State)
	})
}

func (s *Syncer) fetchMailboxes(ctx context.Context, accountID string, ids []jmap.ID) ([]store.Mailbox, error) {
	var out []store.Mailbox
	for _, batch := range chunk(ids, s.maxObjectsInGet) {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&mailbox.Get{Account: s.server(accountID), IDs: batch})

		resp, err := s.do(req)
		if err != nil {
			return nil, err
		}
		got, ok := resp.Responses[0].Args.(*mailbox.GetResponse)
		if !ok {
			return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to Mailbox/get"}
		}
		for _, m := range got.List {
			out = append(out, toStoreMailbox(m))
		}
	}
	return out, nil
}

// syncEmails 同步邮件元数据。
func (s *Syncer) syncEmails(ctx context.Context, accountID string) error {
	localState, err := s.store.State(ctx, accountID, store.TypeEmail)
	if err != nil {
		return err
	}

	if localState == "" {
		return s.bootstrapEmails(ctx, accountID)
	}

	// hasMoreChanges 为真时服务端只给了一部分变更, 必须带着新 state 继续要,
	// 直到取空为止 —— 否则剩下的变更要等下一次推送才会被发现。
	for {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Changes{
			Account:    s.server(accountID),
			SinceState: localState,
			MaxChanges: uint64(s.maxObjectsInGet),
		})

		resp, err := s.do(req)
		if err != nil {
			if methodErrorType(err) == errCannotCalculateChanges {
				s.log.Warn("email state expired, re-bootstrapping", "account", accountID)
				if err := s.rebootstrap(ctx, accountID, store.TypeEmail, s.bootstrapEmails); err != nil {
					return err
				}
				// 离线期间在别处删除的邮件不会出现在任何增量里, 对账清掉。
				if _, err := s.ReconcileEmails(ctx, accountID); err != nil {
					s.log.Warn("reconcile emails", "account", accountID, "err", err)
				}
				return nil
			}
			return err
		}

		changes, ok := resp.Responses[0].Args.(*email.ChangesResponse)
		if !ok {
			return &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/changes"}
		}

		created, err := s.fetchEmails(ctx, accountID, changes.Created)
		if err != nil {
			return err
		}
		updated, err := s.fetchEmails(ctx, accountID, changes.Updated)
		if err != nil {
			return err
		}

		destroyed := idsToStrings(changes.Destroyed)
		if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			if err := store.UpsertEmails(ctx, tx, accountID, created); err != nil {
				return err
			}
			if err := store.UpsertEmails(ctx, tx, accountID, updated); err != nil {
				return err
			}
			if err := store.DeleteEmails(ctx, tx, accountID, destroyed); err != nil {
				return err
			}
			return store.SetState(ctx, tx, accountID, store.TypeEmail, changes.NewState)
		}); err != nil {
			return err
		}

		// 通知只针对新建的邮件, 且必须在数据落库之后 —— 用户点开通知时
		// 那封邮件必须已经能从本地读出来。
		if len(created) > 0 && s.OnNewMail != nil {
			s.OnNewMail(accountID, created)
		}

		localState = changes.NewState
		if !changes.HasMoreChanges {
			return nil
		}
	}
}

// bootstrapEmails 首次同步: 取最近的一个窗口, 并记录服务端给出的完整 state。
//
// 记录的 state 覆盖整个账号而不只是这个窗口, 这是有意的 —— 后续增量能覆盖全部邮件,
// 窗口之外的历史邮件由界面滚动时按需分页补齐, 不影响增量的正确性。
func (s *Syncer) bootstrapEmails(ctx context.Context, accountID string) error {
	req := &jmap.Request{Context: ctx}
	queryID := req.Invoke(&email.Query{
		Account: s.server(accountID),
		Sort: []*email.SortComparator{
			{Property: "receivedAt", IsAscending: false},
		},
		Limit: bootstrapWindow,
	})
	// 用 back-reference 把 query 的结果直接喂给 get, 省掉一轮往返。
	req.Invoke(&email.Get{
		Account:        s.server(accountID),
		ReferenceIDs:   &jmap.ResultReference{ResultOf: queryID, Name: "Email/query", Path: "/ids"},
		Properties:     emailMetaProperties,
		BodyProperties: emailBodyProperties,
	})

	resp, err := s.do(req)
	if err != nil {
		return err
	}
	if len(resp.Responses) < 2 {
		return &Error{Code: CodeUnhandled, Msg: "incomplete response to bootstrap request"}
	}

	got, ok := resp.Responses[1].Args.(*email.GetResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/get"}
	}

	emails := make([]store.Email, 0, len(got.List))
	for _, e := range got.List {
		emails = append(emails, toStoreEmail(e))
	}

	s.log.Info("bootstrapped emails", "account", accountID, "count", len(emails))

	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := store.UpsertEmails(ctx, tx, accountID, emails); err != nil {
			return err
		}
		return store.SetState(ctx, tx, accountID, store.TypeEmail, got.State)
	})
}

func (s *Syncer) fetchEmails(ctx context.Context, accountID string, ids []jmap.ID) ([]store.Email, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]store.Email, 0, len(ids))
	for _, batch := range chunk(ids, s.maxObjectsInGet) {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Get{
			Account:        s.server(accountID),
			IDs:            batch,
			Properties:     emailMetaProperties,
			BodyProperties: emailBodyProperties,
		})

		resp, err := s.do(req)
		if err != nil {
			return nil, err
		}
		got, ok := resp.Responses[0].Args.(*email.GetResponse)
		if !ok {
			return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/get"}
		}
		for _, e := range got.List {
			out = append(out, toStoreEmail(e))
		}
	}
	return out, nil
}

// rebootstrap 丢弃失效的 state 后重新引导。清 state 与重新引导必须分两个事务:
// 引导内部会自行开启事务写入新的 state。
func (s *Syncer) rebootstrap(
	ctx context.Context,
	accountID string,
	dt store.DataType,
	boot func(context.Context, string) error,
) error {
	if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.ResetState(ctx, tx, accountID, dt)
	}); err != nil {
		return err
	}
	return boot(ctx, accountID)
}

func toStoreMailbox(m *mailbox.Mailbox) store.Mailbox {
	return store.Mailbox{
		ID:            string(m.ID),
		ParentID:      string(m.ParentID),
		Name:          m.Name,
		Role:          string(m.Role),
		SortOrder:     int64(m.SortOrder),
		TotalEmails:   int64(m.TotalEmails),
		UnreadEmails:  int64(m.UnreadEmails),
		TotalThreads:  int64(m.TotalThreads),
		UnreadThreads: int64(m.UnreadThreads),
		MyRights:      mailboxRights(m),
		IsSubscribed:  m.IsSubscribed,
	}
}

// mailboxRights 把上游的定长权限结构摊平成 map。
//
// 存成 map 而非固定列, 是因为共享邮箱的权限集合会随服务端演进增删
// (Stalwart 的共享能力仍在扩展), 固定列意味着每加一项权限都要走一次 schema 迁移。
// 权限只用于界面禁用操作, 不参与查询, 不需要列级索引。
func mailboxRights(m *mailbox.Mailbox) map[string]bool {
	if m.Rights == nil {
		return map[string]bool{}
	}
	return map[string]bool{
		"mayReadItems":   m.Rights.MayReadItems,
		"mayAddItems":    m.Rights.MayAddItems,
		"mayRemoveItems": m.Rights.MayRemoveItems,
		"maySetSeen":     m.Rights.MaySetSeen,
		"maySetKeywords": m.Rights.MaySetKeywords,
		"mayCreateChild": m.Rights.MayCreateChild,
		"mayRename":      m.Rights.MayRename,
		"mayDelete":      m.Rights.MayDelete,
		"maySubmit":      m.Rights.MaySubmit,
	}
}

func toStoreEmail(e *email.Email) store.Email {
	out := store.Email{
		ID:            string(e.ID),
		BlobID:        string(e.BlobID),
		ThreadID:      string(e.ThreadID),
		Subject:       e.Subject,
		From:          toStoreAddresses(e.From),
		To:            toStoreAddresses(e.To),
		CC:            toStoreAddresses(e.CC),
		BCC:           toStoreAddresses(e.BCC),
		ReplyTo:       toStoreAddresses(e.ReplyTo),
		SentAt:        e.SentAt,
		Size:          int64(e.Size),
		Preview:       e.Preview,
		HasAttachment: e.HasAttachment,
	}
	if e.BodyStructure != nil {
		out.AttachmentNames = attachmentNames(e.BodyStructure, nil)
	}
	if isSigned(e.BodyStructure) {
		out.Signed = true
		// 服务器把 smime.p7s 算作附件; 只有签名之外还有真正的附件时才显示回形针。
		out.HasAttachment = e.HasAttachment && hasRealAttachment(e.BodyStructure)
	}

	if e.ReceivedAt != nil {
		out.ReceivedAt = *e.ReceivedAt
	}
	if len(e.MessageID) > 0 {
		out.MessageID = e.MessageID[0]
	}
	if len(e.InReplyTo) > 0 {
		out.InReplyTo = e.InReplyTo[0]
	}

	for id, in := range e.MailboxIDs {
		if in {
			out.MailboxIDs = append(out.MailboxIDs, string(id))
		}
	}
	for kw, set := range e.Keywords {
		if set {
			out.Keywords = append(out.Keywords, kw)
		}
	}
	return out
}

func toStoreAddresses(in []*mail.Address) []store.Address {
	out := make([]store.Address, 0, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		out = append(out, store.Address{Name: a.Name, Email: a.Email})
	}
	return out
}

func idsToStrings(ids []jmap.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

func isSignaturePart(t string) bool {
	t = strings.ToLower(t)
	return t == "application/pkcs7-signature" || t == "application/x-pkcs7-signature"
}

// isSigned 判断邮件是否带 S/MIME 签名: 顶层是 multipart/signed, 或不透明签名 (application/pkcs7-mime)。
func isSigned(p *email.BodyPart) bool {
	if p == nil {
		return false
	}
	t := strings.ToLower(p.Type)
	return t == "multipart/signed" || t == "application/pkcs7-mime" || t == "application/x-pkcs7-mime"
}

// attachmentNames 收集结构树里附件的文件名, 判断标准同 hasRealAttachment; 没有文件名的附件不列出。
// 返回值非 nil (没有附件时是空切片), 落库时与"还没取过结构"的 NULL 区分开。
func attachmentNames(p *email.BodyPart, out []string) []string {
	if out == nil {
		out = []string{}
	}
	if p == nil {
		return out
	}
	if len(p.SubParts) > 0 {
		for _, sp := range p.SubParts {
			out = attachmentNames(sp, out)
		}
		return out
	}
	if hasRealAttachment(p) && strings.TrimSpace(p.Name) != "" {
		out = append(out, strings.TrimSpace(p.Name))
	}
	return out
}

// hasRealAttachment 判断结构树里除签名部件外还有没有附件: 标为 attachment 的部件,
// 或没有标 inline 的非文本叶子 (内嵌图片标 inline, 不算)。
func hasRealAttachment(p *email.BodyPart) bool {
	if p == nil {
		return false
	}
	if len(p.SubParts) > 0 {
		for _, sp := range p.SubParts {
			if hasRealAttachment(sp) {
				return true
			}
		}
		return false
	}
	t := strings.ToLower(p.Type)
	if isSignaturePart(t) || strings.HasPrefix(t, "multipart/") {
		return false
	}
	switch strings.ToLower(p.Disposition) {
	case "attachment":
		return true
	case "inline":
		return false
	}
	return !strings.HasPrefix(t, "text/")
}
