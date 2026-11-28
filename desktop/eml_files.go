package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/emlfile"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 打开 .eml: 本机文件 (文件关联、拖进窗口) 与邮件里的 .eml 附件都在本地解析 (internal/emlfile), 不经过邮件服务器。
//
// 解析结果登记在内存里, 邮件 id 是 "eml:<原文摘要>", 第 i 个部件的 blob id 是 "eml:<摘要>:<i>", 原文的 blob id 与邮件 id 相同。
// 阅读栏照常按 账号 + 邮件 id 读邮件、按 blob id 取附件与内嵌图片, 这些路径遇到 eml: 前缀时从这里取。
// 同一份原文得到同一个 id, 译文缓存可以复用。回复、转发引用到的部件由 TransferAttachments 在那时上传到写信账号。

const emlPrefix = "eml:"

// 内存里最多保留 emlKeep 封、合计 emlKeepBytes (原文加解码后的部件); 更早打开的被挤掉后,
// 阅读栏再读会报 2450, 重新打开文件即可。最新打开的那封总是保留。
const (
	emlKeep      = 16
	emlKeepBytes = 256 << 20
)

func isEmlID(id string) bool { return strings.HasPrefix(id, emlPrefix) }

type emlEntry struct {
	msg *emlfile.Message
	// accountID 是打开时所在的账号: 附件里的 .eml 用那封邮件的账号, 本机文件用主登录的个人账号。回复时的发件身份取自它。
	accountID string
	// seq 是打开的先后, 超出上限时挤掉最小的。
	seq  uint64
	size int64
}

type emlRegistry struct {
	mu    sync.Mutex
	items map[string]*emlEntry
	seq   uint64
}

func (r *emlRegistry) put(token string, e *emlEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = map[string]*emlEntry{}
	}
	r.seq++
	e.seq = r.seq
	e.size = int64(len(e.msg.Raw))
	for _, p := range e.msg.Parts {
		e.size += int64(len(p.Data))
	}
	r.items[token] = e
	total := func() (n int64) {
		for _, v := range r.items {
			n += v.size
		}
		return n
	}
	for len(r.items) > 1 && (len(r.items) > emlKeep || total() > emlKeepBytes) {
		oldest := ""
		for k, v := range r.items {
			if oldest == "" || v.seq < r.items[oldest].seq {
				oldest = k
			}
		}
		delete(r.items, oldest)
	}
}

func (r *emlRegistry) get(token string) *emlEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.items[token]
}

// OpenEmlFile 解析本机的 .eml 文件, 返回阅读栏要显示的邮件。
func (a *App) OpenEmlFile(path string) (*store.EmailDetail, error) {
	raw, err := readLimited(path, emlfile.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("2452 %w", err)
	}
	acc := ""
	if st, err := a.currentStore(); err == nil {
		acc = a.personalAccountID(st)
	}
	return a.registerEml(raw, acc)
}

// OpenEmlAttachment 解析邮件里的 .eml 附件 (message/rfc822)。附件必须属于这封邮件, 不接受任意 blob id。
func (a *App) OpenEmlAttachment(accountID, emailID, blobID string) (*store.EmailDetail, error) {
	detail, err := a.emailDetail(accountID, emailID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, att := range detail.Attachments {
		found = found || att.BlobID == blobID
	}
	if !found {
		return nil, fmt.Errorf("2082 attachment not found in this email")
	}
	rc, err := a.openBlob(accountID, blobID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, emlfile.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("2072 download attachment: %w", err)
	}
	return a.registerEml(raw, accountID)
}

func (a *App) registerEml(raw []byte, accountID string) (*store.EmailDetail, error) {
	if len(raw) > emlfile.MaxBytes {
		return nil, fmt.Errorf("2452 email file larger than %d MB", emlfile.MaxBytes>>20)
	}
	msg, err := emlfile.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("2451 %w", err)
	}
	sum := sha256.Sum256(raw)
	token := hex.EncodeToString(sum[:12])
	a.emls.put(token, &emlEntry{msg: msg, accountID: accountID})
	return a.emlDetail(emlPrefix + token)
}

// emlParts 拆开 "eml:<摘要>[:<部件>]", part 为 -1 表示原文。
func (a *App) emlParts(id string) (*emlEntry, string, int, error) {
	rest := strings.TrimPrefix(id, emlPrefix)
	token, partStr, hasPart := strings.Cut(rest, ":")
	e := a.emls.get(token)
	if e == nil {
		return nil, "", 0, fmt.Errorf("2450 email file is no longer open")
	}
	if !hasPart {
		return e, token, -1, nil
	}
	n, err := strconv.Atoi(partStr)
	if err != nil || n < 0 || n >= len(e.msg.Parts) {
		return nil, "", 0, fmt.Errorf("2082 attachment not found in this email")
	}
	return e, token, n, nil
}

func (a *App) emlDetail(emailID string) (*store.EmailDetail, error) {
	e, token, part, err := a.emlParts(emailID)
	if err != nil {
		return nil, err
	}
	if part >= 0 {
		return nil, fmt.Errorf("2450 email file is no longer open")
	}
	id := emlPrefix + token
	d := e.msg.Detail(e.accountID, id, id, func(i int) string { return id + ":" + strconv.Itoa(i) })
	// 不给退订: 文件里的邮件多半不是寄给自己的 (转来的、别人存下的), 退订会替原收件人退订。
	// 非空的零值让界面不再去补查退订头。
	d.Unsubscribe = &store.Unsubscribe{}
	return &d, nil
}

func (a *App) emlBlob(blobID string) ([]byte, error) {
	e, _, part, err := a.emlParts(blobID)
	if err != nil {
		return nil, err
	}
	if part < 0 {
		return e.msg.Raw, nil
	}
	return e.msg.Parts[part].Data, nil
}

func (a *App) emlHeaderInfo(emailID string) (syncer.HeaderInfo, error) {
	e, _, _, err := a.emlParts(emailID)
	if err != nil {
		return syncer.HeaderInfo{}, err
	}
	info := syncer.ParseHeaderInfo(e.msg.Headers)
	if !e.msg.Date.IsZero() {
		t := e.msg.Date
		info.SentAt = &t
	}
	return info, nil
}

// emailDetail 按 账号 + 邮件 id 读邮件: .eml 从内存取, 其余从缓存库取。
func (a *App) emailDetail(accountID, emailID string) (*store.EmailDetail, error) {
	if isEmlID(emailID) {
		return a.emlDetail(emailID)
	}
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Email(a.ctx, accountID, emailID)
}

// openBlob 打开一个 blob: .eml 的部件从内存取, 其余从服务器下载。
func (a *App) openBlob(accountID, blobID string) (io.ReadCloser, error) {
	if isEmlID(blobID) {
		data, err := a.emlBlob(blobID)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	s, err := a.syncerFor(accountID)
	if err != nil {
		return nil, err
	}
	return s.DownloadBlob(a.ctx, accountID, blobID)
}
