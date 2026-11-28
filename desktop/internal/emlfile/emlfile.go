// Package emlfile 在本地解析 .eml 原文 (RFC 5322 / MIME), 给阅读栏打开本机文件与邮件里的 .eml 附件用。
//
// 不经过邮件服务器: 打开一个文件不该要求联网, 文件也不出本机。拆分规则与 syncer 取服务器正文时一致:
// HTML 正文只收 text/html 部件, 纯文本只收 text/plain; 只有 HTML 时由 htmltext 转出纯文本;
// 带 Content-ID 且没有声明为附件的部件是正文里的内嵌图片。
package emlfile

import (
	"bytes"
	"fmt"
	"io"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"git.sr.ht/~rockorager/go-jmap/mail/email"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // 注册 GBK、Big5、Shift_JIS 等字符集
	"github.com/emersion/go-message/mail"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/woodchen-ink/czlmail/desktop/internal/htmltext"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// MaxBytes 是能打开的 .eml 大小上限, 与导入 .eml 相同。
const MaxBytes = 64 << 20

// maxBodyBytes 是正文 (纯文本与 HTML 各自) 的上限, 与 syncer 取服务器正文相同: 再大阅读栏也渲染不完。
const maxBodyBytes = 1 << 20

// Part 是一个附件或内嵌部件, Data 是解码后的内容。
type Part struct {
	Type   string
	Name   string
	CID    string
	Inline bool
	Data   []byte
}

// Message 是解析结果。Parts 的下标就是部件编号, 阅读栏经它取附件与内嵌图片。
type Message struct {
	Raw       []byte
	Subject   string
	From      []store.Address
	To        []store.Address
	CC        []store.Address
	BCC       []store.Address
	ReplyTo   []store.Address
	Date      time.Time
	MessageID string
	InReplyTo string
	Text      string
	HTML      string
	Parts     []Part
	// Headers 是顶层邮件头, 按出现顺序 (最上面的是最后一跳加的), 供退订头与「查看详情」解析。
	Headers []*email.Header
	// Signed 表示带 S/MIME 签名 (multipart/signed 或不透明签名)。
	Signed bool
}

// Parse 解析一封邮件的原文。尽量多显示: 字符集或传输编码不认识的部件按原样保留,
// 损坏的部件 (base64 有误、文件被截断、缺分隔符) 留下读到的部分, 之后的部件读不出就停在那里;
// 只有邮件头都读不出来才算失败。
func Parse(raw []byte) (*Message, error) {
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("email file larger than %d MB", MaxBytes>>20)
	}
	root, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, fmt.Errorf("parse email: %w", err)
	}
	if root.Header.Len() == 0 {
		return nil, fmt.Errorf("parse email: no headers")
	}

	h := mail.Header{Header: root.Header}
	m := &Message{Raw: raw}
	m.Subject, _ = h.Subject()
	m.Subject = fixText(m.Subject)
	m.From = addresses(h, "From")
	m.To = addresses(h, "To")
	m.CC = addresses(h, "Cc")
	m.BCC = addresses(h, "Bcc")
	m.ReplyTo = addresses(h, "Reply-To")
	m.Date = messageDate(h)
	m.MessageID, _ = h.MessageID()
	if ids, err := h.MsgIDList("In-Reply-To"); err == nil && len(ids) > 0 {
		m.InReplyTo = ids[0]
	}
	for f := root.Header.Fields(); f.Next(); {
		m.Headers = append(m.Headers, &email.Header{Name: f.Key(), Value: " " + f.Value()})
	}
	top, _, _ := root.Header.ContentType()
	m.Signed = isSignedType(top)

	var texts, htmls []string
	// 走到损坏处 Walk 返回错误, 已经读到的内容照常显示, 错误不往外报。
	_ = root.Walk(func(_ []int, part *message.Entity, err error) error {
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			return err
		}
		if part.MultipartReader() != nil {
			return nil
		}
		ct, params, _ := part.Header.ContentType()
		ct = strings.ToLower(ct)
		if ct == "" {
			ct = "text/plain"
		}
		disp, dparams, _ := part.Header.ContentDisposition()
		ah := mail.AttachmentHeader{Header: part.Header}
		name, _ := ah.Filename()
		if name == "" {
			name = params["name"]
		}
		if name == "" {
			name = dparams["filename"]
		}
		name = fixText(name)
		cid := strings.Trim(part.Header.Get("Content-Id"), "<> ")
		attached := strings.EqualFold(disp, "attachment") || (name != "" && cid == "")

		if !attached && (ct == "text/plain" || ct == "text/html") {
			body, rerr := io.ReadAll(io.LimitReader(part.Body, maxBodyBytes))
			if ct == "text/html" {
				htmls = append(htmls, string(body))
			} else {
				texts = append(texts, string(body))
			}
			return rerr
		}

		data, rerr := io.ReadAll(part.Body)
		if ct == "message/rfc822" && name == "" {
			name = innerSubject(data) + ".eml"
		}
		m.Parts = append(m.Parts, Part{
			Type:   ct,
			Name:   name,
			CID:    cid,
			Inline: cid != "" && !strings.EqualFold(disp, "attachment"),
			Data:   data,
		})
		return rerr
	})
	if len(texts) == 0 && len(htmls) == 0 && len(m.Parts) == 0 {
		// 连一个部件都没拆出来 (比如 multipart 却没有分隔符): 邮件头之后的内容当纯文本显示。
		if _, body, ok := bytes.Cut(raw, []byte("\r\n\r\n")); ok {
			texts = append(texts, fixText(string(body[:min(len(body), maxBodyBytes)])))
		} else if _, body, ok := bytes.Cut(raw, []byte("\n\n")); ok {
			texts = append(texts, fixText(string(body[:min(len(body), maxBodyBytes)])))
		}
	}

	m.Text = strings.Join(texts, "\n")
	m.HTML = strings.Join(htmls, "\n")
	if m.Text == "" && m.HTML != "" {
		m.Text = htmltext.Convert(m.HTML)
	}
	return m, nil
}

// Detail 转成阅读栏用的邮件详情。id 是邮件的本地标识, blobID(i) 给出第 i 个部件的标识, raw 是原文的标识。
func (m *Message) Detail(accountID, id, raw string, blobID func(i int) string) store.EmailDetail {
	d := store.EmailDetail{
		EmailSummary: store.EmailSummary{
			AccountID:  accountID,
			ID:         id,
			Subject:    m.Subject,
			From:       m.From,
			To:         m.To,
			ReceivedAt: m.Date,
			Preview:    preview(m.Text),
			Signed:     m.Signed,
			Size:       int64(len(m.Raw)),
		},
		CC:          m.CC,
		BCC:         m.BCC,
		ReplyTo:     m.ReplyTo,
		BlobID:      raw,
		MessageID:   m.MessageID,
		InReplyTo:   m.InReplyTo,
		BodyText:    m.Text,
		BodyHTML:    m.HTML,
		BodyFetched: true,
		Attachments: make([]store.Attachment, 0, len(m.Parts)),
		// 文件里的邮件没有地方记 $mdnsent, 也不该替它发已读回执。
		MDNSent: true,
	}
	for i, p := range m.Parts {
		d.Attachments = append(d.Attachments, store.Attachment{
			BlobID: blobID(i),
			Name:   p.Name,
			Type:   p.Type,
			Size:   int64(len(p.Data)),
			CID:    p.CID,
			Inline: p.Inline,
		})
		if !p.Inline && !isSignaturePart(p.Type) {
			d.HasAttachment = true
			if p.Name != "" {
				d.AttachmentNames = append(d.AttachmentNames, p.Name)
			}
		}
	}
	return d
}

// addresses 读地址头。格式不规范解析不了时 (常见于没按 RFC 2047 编码、直接写 GBK 的旧邮件),
// 仍把其中的邮箱地址取出来, 其余文字作为名字, 回复时才有收件人。
func addresses(h mail.Header, key string) []store.Address {
	list, err := h.AddressList(key)
	if err == nil {
		out := make([]store.Address, 0, len(list))
		for _, a := range list {
			out = append(out, store.Address{Name: fixText(a.Name), Email: a.Address})
		}
		return out
	}
	text, _ := h.Text(key)
	if strings.TrimSpace(text) == "" {
		text = h.Get(key)
	}
	out := []store.Address{}
	for _, item := range strings.Split(fixText(text), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		addr := addrPattern.FindString(item)
		name := strings.Trim(strings.TrimSpace(strings.Replace(item, addr, "", 1)), `"<> `)
		out = append(out, store.Address{Name: name, Email: strings.Trim(addr, "<>")})
	}
	return out
}

var addrPattern = regexp.MustCompile(`<?[^\s<>@",;]+@[^\s<>@",;]+>?`)

// fixText 处理没有声明字符集的 8 位文字: 不是合法 UTF-8 时按 GB18030 (GBK 的超集) 解码, 中文旧邮件多是这种。
func fixText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	if out, err := simplifiedchinese.GB18030.NewDecoder().String(s); err == nil && utf8.ValidString(out) {
		return out
	}
	return strings.ToValidUTF8(s, "�")
}

// messageDate 取 Date 头; 没有或写错时退回最上面一条 Received 头分号后的时间 (自己的服务器收到的时间)。
func messageDate(h mail.Header) time.Time {
	// 没有 Date 头时 go-message 返回零值且不报错。
	if d, err := h.Date(); err == nil && !d.IsZero() {
		return d
	}
	if recv := h.Values("Received"); len(recv) > 0 {
		if i := strings.LastIndex(recv[0], ";"); i >= 0 {
			if d, err := netmail.ParseDate(strings.TrimSpace(recv[0][i+1:])); err == nil {
				return d
			}
		}
	}
	return time.Time{}
}

// innerSubject 是转发来的邮件 (message/rfc822) 没有文件名时用的名字: 它自己的主题, 读不出时用 message。
func innerSubject(raw []byte) string {
	if e, err := message.Read(bytes.NewReader(raw)); err == nil || message.IsUnknownCharset(err) {
		h := mail.Header{Header: e.Header}
		if subj, _ := h.Subject(); strings.TrimSpace(subj) != "" {
			return strings.TrimSpace(fixText(subj))
		}
	}
	return "message"
}

func preview(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(s) <= 200 {
		return s
	}
	return string([]rune(s)[:200])
}

func isSignedType(t string) bool {
	t = strings.ToLower(t)
	return t == "multipart/signed" || t == "application/pkcs7-mime" || t == "application/x-pkcs7-mime"
}

func isSignaturePart(t string) bool {
	return t == "application/pkcs7-signature" || t == "application/x-pkcs7-signature"
}
