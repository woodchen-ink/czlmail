package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 附件落地到本地目录后交给系统程序打开。
//
// 目录优先放在程序安装目录下的 attachments: 用户找得到, 卸载时一并清掉。
// 安装在不可写位置(旧版装进了 Program Files、macOS 的 .app 包内)时退回用户缓存目录。
//
// 每封邮件一个子目录, 同名附件不会互相覆盖; 已下载过的直接复用, 再次点击秒开。

const attachmentsDirName = "attachments"

// attachmentsRoot 返回附件根目录, 必要时创建。
func attachmentsRoot() (string, error) {
	return localDataDir(attachmentsDirName)
}

// localDataDir 返回安装目录下的子目录, 不可写时退回用户缓存目录。
func localDataDir(name string) (string, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), name)
		if writableDir(dir) {
			return dir, nil
		}
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("2081 locate %s dir: %w", name, err)
	}
	dir := filepath.Join(base, dataDirName(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("2081 create %s dir: %w", name, err)
	}
	return dir, nil
}

// writableDir 以实际写一个探针文件为准: 目录权限位在 Windows 上说明不了什么。
func writableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// LocalAttachment 是落地后的附件。
type LocalAttachment struct {
	Path string `json:"path"`
	// Revealed 为真表示该文件类型可执行, 没有直接打开, 而是在文件夹中选中了它。
	Revealed bool `json:"revealed"`
}

// OpenAttachment 下载(若尚未下载)并用系统默认程序打开附件。
//
// 可执行类附件不直接运行, 改为在文件夹里选中: 邮件附件是投递恶意程序的头号渠道,
// 点一下附件就运行不能是默认行为。
func (a *App) OpenAttachment(accountID, emailID, blobID string) (LocalAttachment, error) {
	path, err := a.localAttachment(accountID, emailID, blobID)
	if err != nil {
		return LocalAttachment{}, err
	}
	if dangerousExt[strings.ToLower(filepath.Ext(path))] {
		return LocalAttachment{Path: path, Revealed: true}, revealPath(path)
	}
	if err := openPath(path); err != nil {
		return LocalAttachment{}, fmt.Errorf("2084 open attachment: %w", err)
	}
	return LocalAttachment{Path: path}, nil
}

// RevealAttachment 下载(若尚未下载)并在文件管理器中选中附件。
func (a *App) RevealAttachment(accountID, emailID, blobID string) (string, error) {
	path, err := a.localAttachment(accountID, emailID, blobID)
	if err != nil {
		return "", err
	}
	return path, revealPath(path)
}

// DownloadAllAttachments 下载一封邮件的全部附件并打开所在文件夹。
func (a *App) DownloadAllAttachments(accountID, emailID string) (string, error) {
	detail, dir, names, err := a.attachmentPlan(accountID, emailID)
	if err != nil {
		return "", err
	}
	var last string
	for i, att := range detail.Attachments {
		if att.Inline {
			continue
		}
		p, err := a.ensureDownloaded(accountID, att.BlobID, filepath.Join(dir, names[i]))
		if err != nil {
			return "", err
		}
		last = p
	}
	if last == "" {
		return "", fmt.Errorf("2082 email has no attachments")
	}
	return dir, revealPath(last)
}

// localAttachment 确保附件已落地并返回路径。
//
// 附件必须属于该邮件: 界面只传 id, 路径和文件名由这里从缓存里取,
// 不接受前端给的文件名, 避免拼出指向附件目录之外的路径。
func (a *App) localAttachment(accountID, emailID, blobID string) (string, error) {
	detail, dir, names, err := a.attachmentPlan(accountID, emailID)
	if err != nil {
		return "", err
	}
	for i, att := range detail.Attachments {
		if att.BlobID == blobID {
			return a.ensureDownloaded(accountID, blobID, filepath.Join(dir, names[i]))
		}
	}
	return "", fmt.Errorf("2082 attachment not found in this email")
}

// attachmentPlan 给出邮件附件的落地目录与各附件的文件名(与 Attachments 下标对应)。
func (a *App) attachmentPlan(accountID, emailID string) (*store.EmailDetail, string, []string, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, "", nil, err
	}
	detail, err := st.Email(a.ctx, accountID, emailID)
	if err != nil {
		return nil, "", nil, err
	}
	root, err := attachmentsRoot()
	if err != nil {
		return nil, "", nil, err
	}

	// 目录名带主题便于用户在文件管理器里认出来, 带 id 保证不同邮件不撞名。
	subject := []rune(safeFilename(detail.Subject))
	if len(subject) > 40 {
		subject = subject[:40]
	}
	folder := strings.TrimRight(string(subject), ". ") +
		" (" + safeComponent(accountID) + "-" + safeComponent(emailID) + ")"
	dir := filepath.Join(root, folder)

	names := make([]string, len(detail.Attachments))
	used := map[string]bool{}
	for i, att := range detail.Attachments {
		names[i] = uniqueName(attachmentFilename(att), used)
	}
	return detail, dir, names, nil
}

// ensureDownloaded 已存在且非空时直接复用; 否则下载到 .part 再改名,
// 中途失败不会留下一个看起来完整、实际被截断的文件。
func (a *App) ensureDownloaded(accountID, blobID, path string) (string, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("2081 create attachment dir: %w", err)
	}

	rc, err := s.DownloadBlob(a.ctx, accountID, blobID)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("2071 create file: %w", err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("2072 download attachment: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("2071 write file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("2071 finalize file: %w", err)
	}
	// 打上来自互联网的标记: Office 以受保护视图打开, SmartScreen 会检查可执行文件。
	markFromInternet(path)
	return path, nil
}

func attachmentFilename(att store.Attachment) string {
	base := filepath.Base(strings.ReplaceAll(att.Name, `\`, "/"))
	if strings.TrimSpace(att.Name) == "" || base == "." || base == "/" {
		base = "attachment"
	}
	name := safeFilename(base)
	if name == "" {
		name = "attachment"
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if reservedWindowsNames[strings.ToUpper(stem)] {
		name = "_" + name
	}
	return name
}

// uniqueName 在同一封邮件内给重名附件加序号: 两个 image.png 不能互相覆盖。
func uniqueName(name string, used map[string]bool) string {
	candidate := name
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; used[strings.ToLower(candidate)]; n++ {
		candidate = stem + " (" + strconv.Itoa(n) + ")" + ext
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

// safeComponent 把服务端 id 变成安全的路径片段。
func safeComponent(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}

var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// dangerousExt 是双击即执行代码的类型, 点击时只在文件夹中选中, 不直接打开。
var dangerousExt = map[string]bool{
	".exe": true, ".com": true, ".scr": true, ".pif": true, ".bat": true, ".cmd": true,
	".msi": true, ".msp": true, ".msix": true, ".appx": true, ".appinstaller": true,
	".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true, ".wsh": true, ".ws": true,
	".ps1": true, ".psm1": true, ".psd1": true, ".hta": true, ".cpl": true, ".msc": true,
	".lnk": true, ".url": true, ".scf": true, ".reg": true, ".inf": true, ".chm": true,
	".jar": true, ".dll": true, ".sys": true, ".application": true, ".gadget": true,
	".iso": true, ".img": true, ".vhd": true, ".vhdx": true, ".xll": true,
	".settingcontent-ms": true, ".library-ms": true, ".search-ms": true,
	".app": true, ".command": true, ".pkg": true, ".dmg": true, ".sh": true,
}
