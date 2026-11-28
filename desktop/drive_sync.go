package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 跨设备同步的数据(模板、AI 与 MCP 设置、账户设置、标签、附加登录)按类别各一个 JSON 文件, 存在个人网盘的 CZL Mail 文件夹里
// (服务端的文件存储, 同时可经 WebDAV 访问)。这里是读写这些文件的公共部分。
//
// 每次同步: 网盘缓存先追上服务器, 记下版本号, 读缓存里的文件合并, 再带这个版本号条件写回 (syncer.SetFilesIfInState)。
// 两台设备同时写时后写的那台被拒, 由 retryDriveSync 重读重合并, 不会盖掉先写的内容 (以前后写的直接覆盖,
// 别的设备刚重设的同步口令会被冲掉)。

const driveSyncFolder = "CZL Mail"

// errNoDriveSync 表示服务器没有文件存储 (不公布 FileNode, 例如把 IMAP 转成 JMAP 的网关)。
// 这时模板与同步的设置只留在本机; 后台同步遇到它不记警告, 否则每次改设置都会重复同一条。
var errNoDriveSync = errors.New("2164 the server has no file storage; templates and synced settings stay on this device")

// driveSyncMu 串行化所有网盘同步: 各同步首次运行时都可能去建 CZL Mail 文件夹,
// 并发执行会建出两个同名文件夹。
var driveSyncMu sync.Mutex

// driveSyncFile 是一次同步读到的远端文件位置, 写回时复用。
type driveSyncFile struct {
	s         *syncer.Syncer
	accountID string
	name      string
	// state 是读到这份内容时网盘的版本号, 写回时作 ifInState。
	state  string
	folder *store.FileNode
	file   *store.FileNode
}

// driveSyncAttempts 是网盘在读写之间变了时整轮重来的次数上限。
const driveSyncAttempts = 3

// retryDriveSync 运行一轮网盘同步 (读、合并、条件写), 写入因网盘变了被拒时整轮重来。
// 每轮重新拿锁、重新读: 合并以对方刚写的内容为准再做一次。
func retryDriveSync(once func() error) error {
	var err error
	for range driveSyncAttempts {
		if err = once(); !errors.Is(err, syncer.ErrFilesChanged) {
			return err
		}
	}
	return err
}

// openDriveSyncFile 定位个人网盘里的 CZL Mail/<name>, 返回文件句柄与当前内容(不存在时为 nil)。
func (a *App) openDriveSyncFile(ctx context.Context, name string) (*driveSyncFile, []byte, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, nil, err
	}
	accountID := a.personalAccountID(st)
	if accountID == "" {
		// 先报未登录: 会话还没建立时缓存里也可能还没有账号。
		if !a.signedIn() {
			return nil, nil, errNotSignedIn
		}
		return nil, nil, errors.New("2160 no personal account")
	}
	s, err := a.syncerFor(accountID)
	if err != nil {
		return nil, nil, err
	}
	if !s.SupportsFiles(accountID) {
		return nil, nil, errNoDriveSync
	}
	// 先让缓存追上服务器: 刚登录的设备缓存里还没有网盘的文件, 别的设备刚写的内容也要读到最新的。
	state, err := s.CatchUpFiles(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	f := &driveSyncFile{s: s, accountID: accountID, name: name, state: state}
	f.folder, _ = st.FileNodeByName(ctx, accountID, "", driveSyncFolder)
	if f.folder != nil {
		f.file, _ = st.FileNodeByName(ctx, accountID, f.folder.ID, name)
	}
	if f.file == nil || f.file.BlobID == "" {
		return f, nil, nil
	}
	rc, err := s.DownloadBlob(ctx, accountID, f.file.BlobID)
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	return f, data, nil
}

// exists 报告远端文件是否已存在。
func (f *driveSyncFile) exists() bool { return f.file != nil }

// write 上传新内容, 文件或文件夹不存在时创建。每一步都带读到时的版本号: 网盘在这之间变了 (包括别的设备
// 先建出了同名文件夹或文件) 时报 syncer.ErrFilesChanged, 由 retryDriveSync 重来。
func (f *driveSyncFile) write(ctx context.Context, data []byte) error {
	blobID, _, _, err := f.s.UploadBlobType(ctx, f.accountID, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	if f.file != nil {
		_, f.state, err = f.s.SetFilesIfInState(ctx, f.accountID, f.state, nil,
			map[string]map[string]any{f.file.ID: {"blobId": blobID, "type": "application/json"}})
		return err
	}
	folderID := ""
	if f.folder != nil {
		folderID = f.folder.ID
	} else {
		created, state, err := f.s.SetFilesIfInState(ctx, f.accountID, f.state,
			map[string]any{"dir": map[string]any{"parentId": nil, "name": driveSyncFolder}}, nil)
		if err != nil {
			return err
		}
		folderID, f.state = created["dir"], state
	}
	_, f.state, err = f.s.SetFilesIfInState(ctx, f.accountID, f.state, map[string]any{"file": map[string]any{
		"parentId": folderID, "name": f.name, "blobId": blobID, "type": "application/json",
	}}, nil)
	return err
}

// syncDriveSoon 在后台把所有经网盘同步的数据各合并一次, 失败只记日志。
func (a *App) syncDriveSoon() {
	a.syncTemplatesSoon()
	a.syncAISettingsSoon()
	a.syncAccountSettingsSoon()
	a.syncMessageTagsSoon()
	a.syncSignInsSoon()
}
