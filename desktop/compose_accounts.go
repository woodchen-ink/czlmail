package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 写信跨登录 (#15): 发件人可以选任一登录的身份。草稿、发送、定时发送与附件都落在所选身份的账号里,
// 换账号时已上传的附件 blob 搬到新账号 (blob 只在上传它的账号可用, 不同服务器之间更不通用)。

// ComposeIdentityGroup 是写信面板发件人下拉里的一组身份, 一个账号一组。
type ComposeIdentityGroup struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	// SignIn 是所属登录的前缀 (主登录为空), Username / Server 用来在下拉里标出是哪个登录。
	SignIn   string `json:"signIn"`
	Username string `json:"username"`
	Server   string `json:"server"`
	Primary  bool   `json:"primary"`
	// Identities 是这个账号的发件身份; 取不到 (登录离线、身份读取失败) 的账号不列。
	Identities []Identity `json:"identities"`
}

// ListComposeIdentities 列出写信时可选的发件身份: 先是写信账号自己的身份 (回复 / 续编草稿沿用的账号),
// 再是其它登录的个人账号的身份, 按登录顺序。只有一个登录时与原来相同, 只有写信账号这一组。
// 同一登录下的其它账号 (共享邮箱) 不列, 与原来一致: 共享邮箱的身份在该邮箱里写信时才出现。
func (a *App) ListComposeIdentities(accountID string) ([]ComposeIdentityGroup, error) {
	accounts, err := a.ListAccounts()
	if err != nil {
		return nil, err
	}
	targets := composeIdentityAccounts(accounts, accountID)
	infos := a.signInInfoByID()
	out := make([]ComposeIdentityGroup, 0, len(targets))
	for i, acc := range targets {
		ids, err := a.ListIdentities(acc.ID)
		if err != nil || len(ids) == 0 {
			if i == 0 && err != nil {
				// 写信账号自己的身份取不到: 与原来一样报错, 面板显示「没有可用的发件身份」。
				return nil, err
			}
			continue
		}
		si := infos[acc.SignIn]
		out = append(out, ComposeIdentityGroup{
			AccountID: acc.ID, AccountName: acc.Name, SignIn: acc.SignIn,
			Username: si.Username, Server: si.Server, Primary: acc.SignIn == accountid.Primary,
			Identities: ids,
		})
	}
	return out, nil
}

// composeIdentityAccounts 选出发件人下拉里列哪些账号: 写信账号, 加上其它登录各自的个人账号。
// accounts 已按登录排好 (ListAccounts)。写信账号不在缓存里时照样排第一, 由调用方去取它的身份。
func composeIdentityAccounts(accounts []store.Account, composeAccount string) []store.Account {
	prefix, _ := accountid.Split(composeAccount)
	first := store.Account{ID: composeAccount, SignIn: prefix}
	for _, acc := range accounts {
		if acc.ID == composeAccount {
			first = acc
			break
		}
	}
	out := []store.Account{first}
	seen := map[string]bool{prefix: true}
	for _, acc := range accounts {
		if !acc.IsPersonal || seen[acc.SignIn] {
			continue
		}
		seen[acc.SignIn] = true
		out = append(out, acc)
	}
	return out
}

// signInInfoByID 是各登录的用户名与服务器, 按前缀索引。
func (a *App) signInInfoByID() map[string]SignInInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := map[string]SignInInfo{}
	for _, si := range a.signInsLocked() {
		out[si.id] = SignInInfo{ID: si.id, Primary: si.id == accountid.Primary, Server: si.cfg.ServerHost, Username: si.cfg.Username}
	}
	return out
}

// TransferAttachments 把附件 (含内嵌图片) 从一个账号搬到另一个账号: 从原账号下载、上传到目标账号, 返回新的引用。
// 写信时换到别的登录或别的账号的发件身份用: 草稿与发送都在目标账号里做, 引用的 blob 必须在那里。
// 文件名、类型与 cid 不变; 有一个搬不过去就整体报错, 调用方保持原样 (面板退回原来的发件人)。
func (a *App) TransferAttachments(fromAccountID, toAccountID string, refs []AttachmentRef) ([]AttachmentRef, error) {
	if fromAccountID == toAccountID || len(refs) == 0 {
		return refs, nil
	}
	src, err := a.syncerFor(fromAccountID)
	if err != nil {
		return nil, err
	}
	dst, err := a.syncerFor(toAccountID)
	if err != nil {
		return nil, err
	}
	return transferBlobs(a.ctx, src, fromAccountID, dst, toAccountID, dst.MaxUploadBytes(), refs)
}

// blobDownloader / blobUploader 是 syncer.Syncer 上搬 blob 用到的两个方法, 单独抽出来便于测试。
type blobDownloader interface {
	DownloadBlob(ctx context.Context, accountID, blobID string) (io.ReadCloser, error)
}

type blobUploader interface {
	UploadBlobType(ctx context.Context, accountID, contentType string, r io.Reader) (blobID, serverType string, size int64, err error)
}

// transferBlobs 逐个把 refs 引用的 blob 从 from 搬到 to。limit 是目标服务器的单次上传上限, 0 表示不限。
// 同一个 blob 被引用多次 (同一张图既是附件又是内嵌) 只搬一次。
func transferBlobs(ctx context.Context, src blobDownloader, from string, dst blobUploader, to string, limit int64, refs []AttachmentRef) ([]AttachmentRef, error) {
	moved := map[string]string{}
	out := make([]AttachmentRef, len(refs))
	for i, ref := range refs {
		out[i] = ref
		if id, ok := moved[ref.BlobID]; ok {
			out[i].BlobID = id
			continue
		}
		id, err := transferBlob(ctx, src, from, dst, to, limit, ref)
		if err != nil {
			return nil, err
		}
		moved[ref.BlobID] = id
		out[i].BlobID = id
	}
	return out, nil
}

var errBlobTooLarge = errors.New("exceeds the upload limit of the other server")

func transferBlob(ctx context.Context, src blobDownloader, from string, dst blobUploader, to string, limit int64, ref AttachmentRef) (string, error) {
	rc, err := src.DownloadBlob(ctx, from, ref.BlobID)
	if err != nil {
		return "", fmt.Errorf("2440 attachment %s could not be moved to the other account: %w", ref.Name, err)
	}
	defer rc.Close()
	var r io.Reader = rc
	var capped *capReader
	if limit > 0 {
		capped = &capReader{r: rc, left: limit}
		r = capped
	}
	id, _, _, err := dst.UploadBlobType(ctx, to, ref.Type, r)
	if err != nil {
		// HTTP 客户端不一定把读请求体的错误原样包起来, 以读到的字节数为准。
		if errors.Is(err, errBlobTooLarge) || (capped != nil && capped.left < 0) {
			return "", fmt.Errorf("2441 attachment %s is larger than the other server accepts (%s)", ref.Name, humanSize(limit))
		}
		return "", fmt.Errorf("2440 attachment %s could not be moved to the other account: %w", ref.Name, err)
	}
	return id, nil
}

// capReader 读到超过上限时报错, 让上传中止, 不把超限的文件整个传上去再被服务器拒绝。
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, errBlobTooLarge
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, errBlobTooLarge
	}
	return n, err
}

// replaceDraftElsewhere 在草稿换到别的账号后删掉原账号里的旧草稿。
// 新账号里的草稿已经存好或信已发出, 删不掉只记日志: 旧草稿留在原账号的草稿箱里, 用户能看到也能自己删。
func (a *App) replaceDraftElsewhere(req ComposeRequest) {
	if req.DraftID == "" || req.DraftAccountID == "" || req.DraftAccountID == req.AccountID {
		return
	}
	if err := a.DeleteEmails(req.DraftAccountID, []string{req.DraftID}); err != nil {
		a.log.Warn("delete draft left in previous account", "account", req.DraftAccountID, "err", err)
	}
}

// draftInOtherAccount 报告草稿是否在另一个账号里: 这时新账号里不能按 id 替换它 (那个 id 不属于新账号)。
func draftInOtherAccount(req ComposeRequest) bool {
	return req.DraftAccountID != "" && req.DraftAccountID != req.AccountID
}
