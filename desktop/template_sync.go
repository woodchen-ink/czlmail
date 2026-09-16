package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 邮件模板跨设备同步。
//
// 模板存成个人账号网盘里的 CZL Mail/email-templates.json, 格式沿用 Bulwark 的导出格式
// (type = webmail-templates), 两边的导出文件可以直接互相导入。在此基础上多一个
// deletedAt 字段表示墓碑, Bulwark 导入时会忽略它。
//
// 合并规则是逐条"最后修改者胜": 比较 updatedAt(墓碑取删除时间)。两台设备同时改同一个
// 模板时后改的覆盖先改的, 对模板这种低频编辑的数据足够。

const (
	templateFolder   = "CZL Mail"
	templateFileName = "email-templates.json"
	templateFileType = "webmail-templates"
	// 墓碑保留 90 天, 足够让长期离线的设备也收到删除。
	tombstoneTTL = 90 * 24 * time.Hour
)

type templateFile struct {
	Version    int             `json:"version"`
	Type       string          `json:"type"`
	ExportedAt string          `json:"exportedAt"`
	Templates  []templateEntry `json:"templates"`
}

type templateEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	IsHTML     bool     `json:"isHTML"`
	Category   string   `json:"category"`
	IsFavorite bool     `json:"isFavorite"`
	DefaultTo  []string `json:"defaultTo,omitempty"`
	DefaultCC  []string `json:"defaultCc,omitempty"`
	DefaultBCC []string `json:"defaultBcc,omitempty"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
	DeletedAt  string   `json:"deletedAt,omitempty"`
}

var templateSyncMu sync.Mutex

// syncTemplatesSoon 在后台同步模板, 失败只记日志。
func (a *App) syncTemplatesSoon() {
	go func() {
		if err := a.SyncTemplates(); err != nil {
			a.log.Warn("sync templates", "err", err)
		}
	}()
}

// SyncTemplates 立即与服务端合并模板。
func (a *App) SyncTemplates() error {
	templateSyncMu.Lock()
	defer templateSyncMu.Unlock()

	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	accountID := a.personalAccountID(st)
	if accountID == "" {
		return errors.New("2160 no personal account")
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()

	folder, _ := st.FileNodeByName(ctx, accountID, "", templateFolder)
	var file *store.FileNode
	if folder != nil {
		file, _ = st.FileNodeByName(ctx, accountID, folder.ID, templateFileName)
	}

	var remote []templateEntry
	if file != nil && file.BlobID != "" {
		rc, err := s.DownloadBlob(ctx, accountID, file.BlobID)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		rc.Close()
		if err != nil {
			return err
		}
		var tf templateFile
		if err := json.Unmarshal(data, &tf); err != nil || tf.Type != templateFileType {
			return fmt.Errorf("2161 %s/%s is not a template file", templateFolder, templateFileName)
		}
		remote = tf.Templates
	}

	local, err := st.TemplatesForSync(ctx)
	if err != nil {
		return err
	}
	merged, localChanges, remoteChanged := mergeTemplates(local, remote, time.Now())

	if len(localChanges) > 0 {
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			for _, t := range localChanges {
				if err := store.PutSyncedTemplate(ctx, tx, t); err != nil {
					return err
				}
			}
			return store.PurgeDeletedTemplates(ctx, tx, time.Now().Add(-tombstoneTTL))
		}); err != nil {
			return err
		}
		a.emit(EventTemplatesChanged)
	}

	if !remoteChanged && file != nil {
		return nil
	}

	data, err := json.MarshalIndent(templateFile{
		Version: 1, Type: templateFileType, ExportedAt: time.Now().UTC().Format(time.RFC3339), Templates: merged,
	}, "", "  ")
	if err != nil {
		return err
	}
	blobID, _, _, err := s.UploadBlob(ctx, accountID, bytes.NewReader(data))
	if err != nil {
		return err
	}

	if file != nil {
		_, err = s.SetPIM(ctx, accountID, jmapx.FileNode, nil,
			map[string]map[string]any{file.ID: {"blobId": blobID, "type": "application/json"}}, nil, nil)
		return err
	}
	folderID := ""
	if folder != nil {
		folderID = folder.ID
	} else {
		created, err := s.SetPIM(ctx, accountID, jmapx.FileNode,
			map[string]any{"dir": map[string]any{"parentId": nil, "name": templateFolder}}, nil, nil, nil)
		if err != nil {
			return err
		}
		folderID = created["dir"]
	}
	_, err = s.SetPIM(ctx, accountID, jmapx.FileNode, map[string]any{"file": map[string]any{
		"parentId": folderID, "name": templateFileName, "blobId": blobID, "type": "application/json",
	}}, nil, nil, nil)
	return err
}

// mergeTemplates 合并本地与远端, 返回合并结果、需要写回本地的条目, 以及远端是否需要更新。
func mergeTemplates(local []store.SyncedTemplate, remote []templateEntry, now time.Time) ([]templateEntry, []store.SyncedTemplate, bool) {
	byID := map[string]store.SyncedTemplate{}
	for _, t := range local {
		byID[t.ID] = t
	}
	seenRemote := map[string]bool{}
	var localChanges []store.SyncedTemplate
	remoteChanged := false

	for _, r := range remote {
		seenRemote[r.ID] = true
		rt := entryToTemplate(r)
		lt, ok := byID[r.ID]
		switch {
		case !ok || stamp(rt) > stamp(lt):
			rt.RemoteRev = lt.RemoteRev
			byID[r.ID] = rt
			localChanges = append(localChanges, rt)
		case stamp(lt) > stamp(rt):
			remoteChanged = true
		}
	}
	for id := range byID {
		if !seenRemote[id] {
			remoteChanged = true
		}
	}

	var out []templateEntry
	cutoff := now.Add(-tombstoneTTL).Unix()
	for _, t := range byID {
		if t.DeletedAt > 0 && t.DeletedAt < cutoff {
			if seenRemote[t.ID] {
				remoteChanged = true
			}
			continue
		}
		out = append(out, templateToEntry(t))
	}
	return out, localChanges, remoteChanged
}

func stamp(t store.SyncedTemplate) int64 {
	return max(t.UpdatedAt, t.DeletedAt)
}

func entryToTemplate(e templateEntry) store.SyncedTemplate {
	t := store.SyncedTemplate{Template: store.Template{
		ID: e.ID, Name: e.Name, Category: e.Category, Subject: e.Subject, Body: e.Body, IsHTML: e.IsHTML,
		IsFavorite: e.IsFavorite, DefaultTo: e.DefaultTo, DefaultCC: e.DefaultCC, DefaultBCC: e.DefaultBCC,
		CreatedAt: parseISO(e.CreatedAt), UpdatedAt: parseISO(e.UpdatedAt),
	}}
	if t.Name == "" {
		t.Name = "(未命名模板)"
	}
	t.DeletedAt = parseISO(e.DeletedAt)
	return t
}

func templateToEntry(t store.SyncedTemplate) templateEntry {
	e := templateEntry{
		ID: t.ID, Name: t.Name, Subject: t.Subject, Body: t.Body, IsHTML: t.IsHTML, Category: t.Category,
		IsFavorite: t.IsFavorite, DefaultTo: t.DefaultTo, DefaultCC: t.DefaultCC, DefaultBCC: t.DefaultBCC,
		CreatedAt: isoTime(t.CreatedAt), UpdatedAt: isoTime(t.UpdatedAt),
	}
	if t.DeletedAt > 0 {
		e.DeletedAt = isoTime(t.DeletedAt)
	}
	return e
}

func parseISO(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Unix()
	}
	return 0
}

func isoTime(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// ImportTemplatesFile 从本地 JSON 文件(本程序或 Bulwark 的模板导出)导入模板, 返回导入条数。
// 同 id 的模板按修改时间合并, 不会产生重复。
func (a *App) ImportTemplatesFile() (int, error) {
	st, err := a.currentStore()
	if err != nil {
		return 0, err
	}
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:   "导入邮件模板",
		Filters: []wruntime.FileFilter{{DisplayName: "模板文件 (*.json)", Pattern: "*.json"}},
	})
	if err != nil || path == "" {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var tf templateFile
	if err := json.Unmarshal(data, &tf); err != nil || tf.Type != templateFileType {
		return 0, errors.New("2162 not a template export file")
	}
	local, err := st.TemplatesForSync(a.ctx)
	if err != nil {
		return 0, err
	}
	_, changes, _ := mergeTemplates(local, tf.Templates, time.Now())
	if err := st.WithTx(a.ctx, func(tx *sql.Tx) error {
		for _, t := range changes {
			if err := store.PutSyncedTemplate(a.ctx, tx, t); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	a.syncTemplatesSoon()
	return len(changes), nil
}

// EventTemplatesChanged 模板被同步改动, 界面应重新读取。
const EventTemplatesChanged = "templates:changed"
