package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 阅读栏工具条上的其余操作: 归档、垃圾邮件、标签、查看源码、导出 / 导入 .eml。

// ArchiveEmails 移入归档邮箱。
func (a *App) ArchiveEmails(accountID string, emailIDs []string) error {
	target, err := a.mailboxOfKind(accountID, "archive")
	if err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("2031 account has no archive mailbox")
	}
	return a.MoveEmails(accountID, emailIDs, target)
}

// MarkJunk 标记为垃圾邮件(移入垃圾箱), 或从垃圾箱里标记为正常(移回收件箱)。
func (a *App) MarkJunk(accountID string, emailIDs []string, junk bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}

	kind := "inbox"
	if junk {
		kind = "junk"
	}
	target, err := a.mailboxOfKind(accountID, kind)
	if err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("2032 account has no %s mailbox", kind)
	}
	return s.MarkJunk(a.ctx, accountID, emailIDs, target, junk)
}

// SetLabel 给邮件加上或去掉标签。标签即 JMAP 的自定义关键字。
func (a *App) SetLabel(accountID string, emailIDs []string, label string, set bool) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if err := validateLabel(label); err != nil {
		return err
	}
	return s.SetKeyword(a.ctx, accountID, emailIDs, label, set)
}

// labelPattern 是 RFC 8621 对关键字的限制: 可见 ASCII, 且不能含这些字符。
// 中文标签不能直接作为关键字存 —— 服务器会拒绝整个请求。
var labelPattern = regexp.MustCompile(`^[\x21-\x7e]+$`)

func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("2033 label is empty")
	}
	if strings.HasPrefix(label, "$") {
		// $ 开头的是系统关键字($seen / $flagged ...), 不允许当作标签随意改动。
		return fmt.Errorf("2034 labels cannot start with $")
	}
	if !labelPattern.MatchString(label) || strings.ContainsAny(label, `()[]{}%*"\`) {
		return fmt.Errorf("2035 label must be printable ASCII without ( ) { } [ ] %% * \" \\")
	}
	return nil
}

// ListLabels 列出该账号用过的全部标签。
func (a *App) ListLabels(accountID string) ([]string, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Labels(a.ctx, accountID)
}

// ViewSource 返回邮件原文, 供"查看源码"显示。
func (a *App) ViewSource(accountID, emailID string) (string, error) {
	raw, err := a.rawMessage(accountID, emailID)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ExportEmail 把邮件原文保存为 .eml, 返回保存的路径; 用户取消时返回空串。
func (a *App) ExportEmail(accountID, emailID string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	detail, err := st.Email(a.ctx, accountID, emailID)
	if err != nil {
		return "", err
	}

	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "导出邮件",
		DefaultFilename: safeFilename(detail.Subject) + ".eml",
		Filters:         []wruntime.FileFilter{{DisplayName: "邮件 (*.eml)", Pattern: "*.eml"}},
	})
	if err != nil {
		return "", fmt.Errorf("2036 open save dialog: %w", err)
	}
	if path == "" {
		return "", nil
	}

	raw, err := a.rawMessage(accountID, emailID)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("2037 write file: %w", err)
	}
	return path, nil
}

// zip 导入的上限, 防止压缩炸弹: 一个几 KB 的 zip 可以展开成几十 GB。
const (
	maxZipEntries = 5000
	maxEntryBytes = 64 << 20
)

// ImportEmails 选择 .eml 或装着 .eml 的 .zip 导入到指定邮箱, 返回成功导入的封数。
//
// 单封失败不中断整批: 一个几百封的归档包里夹着一两封格式损坏的邮件很常见,
// 为此放弃整批只会逼用户手工拆包。失败数随结果一并返回。
func (a *App) ImportEmails(accountID, mailboxID string) (*ImportResult, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	if mailboxID == "" {
		return nil, fmt.Errorf("2038 no target mailbox selected")
	}

	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "导入邮件",
		Filters: []wruntime.FileFilter{
			{DisplayName: "邮件或压缩包 (*.eml, *.zip)", Pattern: "*.eml;*.zip"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("2039 open file dialog: %w", err)
	}

	res := &ImportResult{}
	importOne := func(raw []byte) {
		if err := s.ImportMessage(a.ctx, accountID, mailboxID, raw, nil); err != nil {
			res.Failed++
			if res.FirstError == "" {
				res.FirstError = err.Error()
			}
			return
		}
		res.Imported++
	}

	for _, path := range paths {
		if strings.EqualFold(filepath.Ext(path), ".zip") {
			if err := forEachZipMessage(path, importOne); err != nil {
				res.Failed++
				if res.FirstError == "" {
					res.FirstError = err.Error()
				}
			}
			continue
		}

		raw, err := readLimited(path, maxEntryBytes)
		if err != nil {
			res.Failed++
			if res.FirstError == "" {
				res.FirstError = err.Error()
			}
			continue
		}
		importOne(raw)
	}

	if res.Imported > 0 {
		_ = s.SyncAccount(a.ctx, accountID)
	}
	return res, nil
}

// ImportResult 是一次导入的结果。
type ImportResult struct {
	Imported   int    `json:"imported"`
	Failed     int    `json:"failed"`
	FirstError string `json:"firstError"`
}

func forEachZipMessage(path string, fn func([]byte)) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("2040 open zip: %w", err)
	}
	defer zr.Close()

	count := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(f.Name), ".eml") {
			continue
		}
		count++
		if count > maxZipEntries {
			return fmt.Errorf("2041 zip contains more than %d messages", maxZipEntries)
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		// 不信任 zip 头里声明的解压后大小, 以实际读出的字节数为准。
		raw, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
		rc.Close()
		if err != nil || len(raw) > maxEntryBytes {
			continue
		}
		fn(raw)
	}
	return nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("2042 open file: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("2042 read file: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("2043 %s exceeds %d MB", filepath.Base(path), limit>>20)
	}
	return raw, nil
}

func (a *App) rawMessage(accountID, emailID string) ([]byte, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	detail, err := st.Email(a.ctx, accountID, emailID)
	if err != nil {
		return nil, err
	}
	return s.RawMessage(a.ctx, accountID, detail.BlobID)
}

func (a *App) mailboxOfKind(accountID, kind string) (string, error) {
	st, err := a.currentStore()
	if err != nil {
		return "", err
	}
	return mailboxByRole(st, a.ctx, accountID, kind)
}

// safeFilename 把邮件主题变成可用的文件名。Windows 对文件名字符与长度都有限制,
// 主题里的冒号、斜杠在 "Re: 合同/报价" 这类邮件里几乎必然出现。
func safeFilename(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "email"
	}
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return strings.TrimRight(s, ". ")
}
