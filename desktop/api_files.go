package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmapx"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 文件(JMAP FileNode)。目录树元数据走本地缓存, 内容按需下载到安装目录下的 files。

const filesDirName = "files"

// ListFiles 列出目录内容, parentID 为空表示根目录。
func (a *App) ListFiles(accountID, parentID string) ([]store.FileNode, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.FileNodes(a.ctx, accountID, parentID)
}

// SearchFiles 按名称检索整个账号的文件。
func (a *App) SearchFiles(accountID, query string) ([]store.FileNode, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.SearchFileNodes(a.ctx, accountID, query, 500)
}

// FilePath 返回从根到该节点的路径(不含根), 用于面包屑。
func (a *App) FilePath(accountID, id string) ([]store.FileNode, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	var out []store.FileNode
	for id != "" && len(out) < 64 {
		n, err := st.FileNodeByID(a.ctx, accountID, id)
		if err != nil {
			break
		}
		out = append([]store.FileNode{*n}, out...)
		id = n.ParentID
	}
	return out, nil
}

// CreateFolder 新建目录。
func (a *App) CreateFolder(accountID, parentID, name string) error {
	name = strings.TrimSpace(name)
	if err := validFileName(name); err != nil {
		return err
	}
	return a.setFiles(accountID, map[string]any{"dir": map[string]any{"parentId": nullable(parentID), "name": name}}, nil, nil)
}

// UploadFiles 选择本地文件上传到目录, 返回上传数量。
func (a *App) UploadFiles(accountID, parentID string) (int, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return 0, err
	}
	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "上传文件"})
	if err != nil {
		return 0, fmt.Errorf("2073 open file dialog: %w", err)
	}
	limit := s.MaxUploadBytes()
	create := map[string]any{}
	for i, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return 0, fmt.Errorf("2074 read %s: %w", filepath.Base(path), err)
		}
		if limit > 0 && info.Size() > limit {
			return 0, fmt.Errorf("2075 %s is %s, exceeds server limit of %s",
				filepath.Base(path), humanSize(info.Size()), humanSize(limit))
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, fmt.Errorf("2074 open %s: %w", filepath.Base(path), err)
		}
		blobID, serverType, _, err := s.UploadBlob(a.ctx, accountID, f)
		f.Close()
		if err != nil {
			return 0, err
		}
		create[fmt.Sprintf("f%d", i)] = map[string]any{
			"parentId": nullable(parentID),
			"name":     filepath.Base(path),
			"blobId":   blobID,
			"type":     attachmentType(path, serverType),
		}
	}
	if len(create) == 0 {
		return 0, nil
	}
	return len(create), a.setFiles(accountID, create, nil, nil)
}

// RenameFile 重命名。
func (a *App) RenameFile(accountID, id, name string) error {
	name = strings.TrimSpace(name)
	if err := validFileName(name); err != nil {
		return err
	}
	return a.setFiles(accountID, nil, map[string]map[string]any{id: {"name": name}}, nil)
}

// MoveFiles 移动到另一个目录, parentID 为空表示根目录。
func (a *App) MoveFiles(accountID string, ids []string, parentID string) error {
	for _, id := range ids {
		if id == parentID {
			return fmt.Errorf("2120 cannot move a folder into itself")
		}
	}
	update := map[string]map[string]any{}
	for _, id := range ids {
		update[id] = map[string]any{"parentId": nullable(parentID)}
	}
	return a.setFiles(accountID, nil, update, nil)
}

// DeleteFiles 删除文件或目录(连同子项)。
func (a *App) DeleteFiles(accountID string, ids []string) error {
	return a.setFiles(accountID, nil, nil, ids)
}

// OpenFile 下载(若尚未下载)并用系统程序打开。可执行文件只在文件夹中选中。
func (a *App) OpenFile(accountID, id string) (LocalAttachment, error) {
	path, err := a.localFile(accountID, id)
	if err != nil {
		return LocalAttachment{}, err
	}
	if dangerousExt[strings.ToLower(filepath.Ext(path))] {
		return LocalAttachment{Path: path, Revealed: true}, revealPath(path)
	}
	if err := openPath(path); err != nil {
		return LocalAttachment{}, fmt.Errorf("2084 open file: %w", err)
	}
	return LocalAttachment{Path: path}, nil
}

// SaveFileAs 另存到用户选择的位置。
func (a *App) SaveFileAs(accountID, id string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	n, err := st.FileNodeByID(a.ctx, accountID, id)
	if err != nil {
		return "", err
	}
	if n.IsDirectory || n.BlobID == "" {
		return "", fmt.Errorf("2121 folders cannot be downloaded")
	}
	return a.SaveAttachment(accountID, n.BlobID, n.Name)
}

// localFile 把文件下载到 <安装目录>/files/<账号>-<id>/<名称>。
// 目录带节点 id: 同名文件在不同目录里不会互相覆盖; 内容更新后 blobId 变化, 用 blob 前缀区分版本。
func (a *App) localFile(accountID, id string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	n, err := st.FileNodeByID(a.ctx, accountID, id)
	if err != nil {
		return "", err
	}
	if n.IsDirectory || n.BlobID == "" {
		return "", fmt.Errorf("2121 folders cannot be opened")
	}
	root, err := localDataDir(filesDirName)
	if err != nil {
		return "", err
	}
	blob := safeComponent(n.BlobID)
	if len(blob) > 12 {
		blob = blob[:12]
	}
	dir := filepath.Join(root, safeComponent(accountID)+"-"+safeComponent(id)+"-"+blob)
	name := attachmentFilename(store.Attachment{Name: n.Name})
	return a.ensureDownloaded(accountID, n.BlobID, filepath.Join(dir, name))
}

func (a *App) setFiles(accountID string, create map[string]any, update map[string]map[string]any, destroy []string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	extra := map[string]any{}
	if len(destroy) > 0 {
		extra["onDestroyRemoveChildren"] = true
	}
	_, err = s.SetPIM(a.ctx, accountID, jmapx.FileNode, create, update, destroy, extra)
	return err
}

func validFileName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("2122 invalid name %q", name)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// UploadPaths 上传本地文件或整个文件夹(递归)到目录, 返回上传的文件数。
// 用于从资源管理器拖入与"上传文件夹"。
func (a *App) UploadPaths(accountID, parentID string, paths []string) (int, error) {
	count := 0
	for _, p := range paths {
		n, err := a.uploadPath(accountID, parentID, p)
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// UploadFolder 选择本地文件夹整个上传。
func (a *App) UploadFolder(accountID, parentID string) (int, error) {
	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "上传文件夹"})
	if err != nil || dir == "" {
		return 0, err
	}
	return a.UploadPaths(accountID, parentID, []string{dir})
}

// maxUploadEntries 防止误拖一个巨大目录(比如整个盘)把网盘塞满。
const maxUploadEntries = 5000

func (a *App) uploadPath(accountID, parentID, path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("2074 read %s: %w", filepath.Base(path), err)
	}
	st, err := a.currentStore()
	if err != nil {
		return 0, err
	}
	name, err := a.freeName(st, accountID, parentID, filepath.Base(path))
	if err != nil {
		return 0, err
	}
	s, err := a.currentSyncer()
	if err != nil {
		return 0, err
	}

	if !info.IsDir() {
		if limit := s.MaxUploadBytes(); limit > 0 && info.Size() > limit {
			return 0, fmt.Errorf("2075 %s is %s, exceeds server limit of %s", name, humanSize(info.Size()), humanSize(limit))
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		blobID, serverType, _, err := s.UploadBlob(a.ctx, accountID, f)
		f.Close()
		if err != nil {
			return 0, err
		}
		return 1, a.setFiles(accountID, map[string]any{"f": map[string]any{
			"parentId": nullable(parentID), "name": name, "blobId": blobID, "type": attachmentType(path, serverType),
		}}, nil, nil)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	if len(entries) > maxUploadEntries {
		return 0, fmt.Errorf("2123 %s has too many entries", name)
	}
	created, err := s.SetPIM(a.ctx, accountID, jmapx.FileNode,
		map[string]any{"d": map[string]any{"parentId": nullable(parentID), "name": name}}, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		n, err := a.uploadPath(accountID, created["d"], filepath.Join(path, e.Name()))
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// freeName 在目标目录里找一个不重名的名字: 报告.docx → 报告 (2).docx。
func (a *App) freeName(st *store.Store, accountID, parentID, name string) (string, error) {
	siblings, err := st.FileNodes(a.ctx, accountID, parentID)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, n := range siblings {
		used[strings.ToLower(n.Name)] = true
	}
	return uniqueName(name, used), nil
}

// CopyFiles 复制文件或目录到目标目录。文件内容共用同一个 blob, 不重新上传。
func (a *App) CopyFiles(accountID string, ids []string, parentID string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := a.copyNode(st, accountID, id, parentID, 0); err != nil {
			return err
		}
	}
	return nil
}

// DuplicateFile 在同一目录里创建副本。
func (a *App) DuplicateFile(accountID, id string) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	n, err := st.FileNodeByID(a.ctx, accountID, id)
	if err != nil {
		return err
	}
	return a.copyNode(st, accountID, id, n.ParentID, 0)
}

func (a *App) copyNode(st *store.Store, accountID, id, parentID string, depth int) error {
	if depth > 32 {
		return fmt.Errorf("2124 folder nesting too deep")
	}
	n, err := st.FileNodeByID(a.ctx, accountID, id)
	if err != nil {
		return err
	}
	// 目录不能复制进它自己或它的子目录。
	for p := parentID; p != ""; {
		if p == id {
			return fmt.Errorf("2120 cannot copy a folder into itself")
		}
		parent, err := st.FileNodeByID(a.ctx, accountID, p)
		if err != nil {
			break
		}
		p = parent.ParentID
	}
	name, err := a.freeName(st, accountID, parentID, n.Name)
	if err != nil {
		return err
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	obj := map[string]any{"parentId": nullable(parentID), "name": name}
	if !n.IsDirectory {
		obj["blobId"] = n.BlobID
		obj["type"] = n.ContentType
	}
	created, err := s.SetPIM(a.ctx, accountID, jmapx.FileNode, map[string]any{"c": obj}, nil, nil, nil)
	if err != nil || !n.IsDirectory {
		return err
	}
	children, err := st.FileNodes(a.ctx, accountID, id)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := a.copyNode(st, accountID, c.ID, created["c"], depth+1); err != nil {
			return err
		}
	}
	return nil
}

// CreateTextFile 新建空白文本文件。
func (a *App) CreateTextFile(accountID, parentID, name string) error {
	name = strings.TrimSpace(name)
	if err := validFileName(name); err != nil {
		return err
	}
	if filepath.Ext(name) == "" {
		name += ".txt"
	}
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	if name, err = a.freeName(st, accountID, parentID, name); err != nil {
		return err
	}
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	blobID, _, _, err := s.UploadBlob(a.ctx, accountID, strings.NewReader(""))
	if err != nil {
		return err
	}
	return a.setFiles(accountID, map[string]any{"f": map[string]any{
		"parentId": nullable(parentID), "name": name, "blobId": blobID, "type": "text/plain",
	}}, nil, nil)
}

// FileFavorites 返回收藏的文件, 键为 "账号/节点id"。收藏只保存在本机。
func (a *App) FileFavorites() ([]store.FileNode, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	keys := a.favoriteKeys(st)
	var out []store.FileNode
	for _, k := range keys {
		acc, id, ok := strings.Cut(k, "/")
		if !ok {
			continue
		}
		if n, err := st.FileNodeByID(a.ctx, acc, id); err == nil {
			out = append(out, *n)
		}
	}
	return out, nil
}

// SetFileFavorite 收藏或取消收藏。
func (a *App) SetFileFavorite(accountID, id string, favorite bool) error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	key := accountID + "/" + id
	var next []string
	for _, k := range a.favoriteKeys(st) {
		if k != key {
			next = append(next, k)
		}
	}
	if favorite {
		next = append(next, key)
	}
	data, _ := json.Marshal(next)
	return st.SetStringSetting(a.ctx, store.SettingFileFavorites, string(data))
}

func (a *App) favoriteKeys(st *store.Store) []string {
	var keys []string
	if raw, err := st.StringSetting(a.ctx, store.SettingFileFavorites); err == nil {
		_ = json.Unmarshal([]byte(raw), &keys)
	}
	return keys
}
