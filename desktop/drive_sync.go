package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 跨设备同步的数据(模板、AI 与 MCP 设置)都以 JSON 文件存在个人网盘的 CZL Mail 文件夹里
// (服务端的文件存储, 同时可经 WebDAV 访问)。这里是读写这些文件的公共部分。

const driveSyncFolder = "CZL Mail"

// driveSyncMu 串行化所有网盘同步: 各同步首次运行时都可能去建 CZL Mail 文件夹,
// 并发执行会建出两个同名文件夹。
var driveSyncMu sync.Mutex

// driveSyncFile 是一次同步读到的远端文件位置, 写回时复用。
type driveSyncFile struct {
	s         *syncer.Syncer
	accountID string
	name      string
	folder    *store.FileNode
	file      *store.FileNode
}

// openDriveSyncFile 定位个人网盘里的 CZL Mail/<name>, 返回文件句柄与当前内容(不存在时为 nil)。
func (a *App) openDriveSyncFile(ctx context.Context, name string) (*driveSyncFile, []byte, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, nil, err
	}
	st, err := a.currentStore()
	if err != nil {
		return nil, nil, err
	}
	accountID := a.personalAccountID(st)
	if accountID == "" {
		return nil, nil, errors.New("2160 no personal account")
	}
	f := &driveSyncFile{s: s, accountID: accountID, name: name}
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

// write 上传新内容, 文件或文件夹不存在时创建。
func (f *driveSyncFile) write(ctx context.Context, data []byte) error {
	blobID, _, _, err := f.s.UploadBlob(ctx, f.accountID, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if f.file != nil {
		_, err = f.s.SetPIM(ctx, f.accountID, jmapx.FileNode, nil,
			map[string]map[string]any{f.file.ID: {"blobId": blobID, "type": "application/json"}}, nil, nil)
		return err
	}
	folderID := ""
	if f.folder != nil {
		folderID = f.folder.ID
	} else {
		created, err := f.s.SetPIM(ctx, f.accountID, jmapx.FileNode,
			map[string]any{"dir": map[string]any{"parentId": nil, "name": driveSyncFolder}}, nil, nil, nil)
		if err != nil {
			return err
		}
		folderID = created["dir"]
	}
	_, err = f.s.SetPIM(ctx, f.accountID, jmapx.FileNode, map[string]any{"file": map[string]any{
		"parentId": folderID, "name": f.name, "blobId": blobID, "type": "application/json",
	}}, nil, nil, nil)
	return err
}

// syncDriveSoon 在后台把所有经网盘同步的数据各合并一次, 失败只记日志。
func (a *App) syncDriveSoon() {
	a.syncTemplatesSoon()
	a.syncAISettingsSoon()
}
