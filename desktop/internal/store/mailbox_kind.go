package store

import (
	"regexp"
	"strings"
)

// 邮箱类别。与 JMAP 的 role 取值一致, 多出的 KindCustom 表示用户自建文件夹。
const (
	KindInbox     = "inbox"
	KindDrafts    = "drafts"
	KindScheduled = "scheduled"
	KindSent      = "sent"
	KindArchive   = "archive"
	KindJunk      = "junk"
	KindTrash     = "trash"
	KindCustom    = ""
)

// kindPatterns 按文件夹名识别类别, 仅在服务器没有给出 role 时使用。
//
// 需要这层兜底: role 是 JMAP 的可选属性, 从 IMAP 迁移过来或由其它客户端创建的
// 文件夹经常没有 role。名称写法五花八门(Outlook 的 "Deleted Items"、Gmail 的
// "[Gmail]/Spam"、中文客户端的"已删除"), 用整名匹配而不是包含匹配 ——
// 用户自建的 "Sent to accountant" 不该被当成已发送。
//
// 未命中任何模式时落到 KindCustom, 不做猜测。
var kindPatterns = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{KindInbox, regexp.MustCompile(`^(inbox|收件箱|收件匣|posteingang)$`)},
	{KindDrafts, regexp.MustCompile(`^(drafts?|草稿|草稿箱|entwürfe)$`)},
	{KindScheduled, regexp.MustCompile(`^(scheduled|已计划|定时发送)$`)},
	{KindSent, regexp.MustCompile(`^(sent|sent items|sent mail|sent messages|已发送|已发送邮件|发件箱|gesendet)$`)},
	{KindArchive, regexp.MustCompile(`^(archives?|all mail|归档|存档|archiv)$`)},
	{KindJunk, regexp.MustCompile(`^(junk|junk mail|junk e-?mail|spam|bulk mail|垃圾邮件|垃圾箱|spam-ordner)$`)},
	{KindTrash, regexp.MustCompile(`^(trash|deleted|deleted items|deleted messages|bin|已删除|已删除邮件|回收站|papierkorb)$`)},
}

// InferMailboxKind 决定邮箱在界面上按哪一类处理。服务器给的 role 优先。
func InferMailboxKind(role, name string) string {
	if role != "" {
		return strings.ToLower(role)
	}

	n := strings.ToLower(strings.TrimSpace(name))
	// 形如 "[Gmail]/Spam" 或 "INBOX.Trash" 的层级名只看最后一段。
	if i := strings.LastIndexAny(n, "/."); i >= 0 && i < len(n)-1 {
		n = n[i+1:]
	}

	for _, p := range kindPatterns {
		if p.pattern.MatchString(n) {
			return p.kind
		}
	}
	return KindCustom
}
