package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 附件: 预览、保存、上传。

// BlobPath 是界面预览附件的本地路径,
// 形如 /czl-blob?account=c&blob=xxx&type=image/png。
const BlobPath = "/czl-blob"

// maxPreviewBytes 限制经预览路径读取的大小。更大的附件只能保存到本地查看。
const maxPreviewBytes = 25 << 20

// previewableTypes 是允许在应用内联显示的类型。
//
// 这是安全边界而不是功能清单: 预览路径与应用同源, 一个按 text/html 或
// image/svg+xml 内联返回的附件可以执行脚本, 进而调用暴露给前端的全部 Go 绑定 ——
// 包括发信与删信。因此只放行不会执行代码的类型, 其余一律按下载处理。
//
// PDF 不在其中: WebView2 的 PDF 查看器加载不了 AssetServer 提供的资源, 交给系统程序打开。
var previewableTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
	"text/plain": true,
}

func normalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.Index(t, ";"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// serveBlob 处理预览请求。
func (a *App) serveBlob(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID, blobID := q.Get("account"), q.Get("blob")
	contentType := normalizeType(q.Get("type"))

	if accountID == "" || blobID == "" {
		http.NotFound(w, r)
		return
	}

	s, err := a.currentSyncer()
	if err != nil {
		http.Error(w, "not signed in", http.StatusServiceUnavailable)
		return
	}

	rc, err := s.DownloadBlob(r.Context(), accountID, blobID)
	if err != nil {
		http.Error(w, "download failed", http.StatusBadGateway)
		return
	}
	defer rc.Close()

	h := w.Header()
	// 不许浏览器按内容嗅探出另一种类型, 否则一个声称是 text/plain 的 HTML 仍会被渲染。
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=600")

	if !previewableTypes[contentType] {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment")
	} else {
		h.Set("Content-Type", contentType)
		// sandbox 让被直接打开的响应处于无脚本、无同源的环境。
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
	}

	if _, err := io.Copy(w, io.LimitReader(rc, maxPreviewBytes)); err != nil {
		a.log.Warn("serve blob", "err", err)
	}
}

// SaveAttachment 把附件保存到用户选择的位置。返回保存路径, 用户取消时返回空串。
func (a *App) SaveAttachment(accountID, blobID, name string) (string, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return "", err
	}

	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "保存附件",
		DefaultFilename: safeFilename(name),
	})
	if err != nil {
		return "", fmt.Errorf("2070 open save dialog: %w", err)
	}
	if path == "" {
		return "", nil
	}

	rc, err := s.DownloadBlob(a.ctx, accountID, blobID)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	// 先写临时文件再改名: 下载中途断网时不留下一个看起来完整、实际被截断的文件。
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
	return path, nil
}

// UploadedAttachment 是已上传到服务器、可被邮件引用的附件。
type UploadedAttachment struct {
	BlobID string `json:"blobId"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}

// AddAttachments 让用户选择文件并上传, 返回可附加到邮件的引用。
//
// 超过服务器上传上限的文件在本地就拒绝, 不去撞服务器 —— 上传几十 MB
// 之后才被告知超限, 等待时间全浪费了。
func (a *App) AddAttachments(accountID string) ([]UploadedAttachment, error) {
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}

	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "添加附件"})
	if err != nil {
		return nil, fmt.Errorf("2073 open file dialog: %w", err)
	}

	limit := s.MaxUploadBytes()
	out := make([]UploadedAttachment, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return out, fmt.Errorf("2074 read %s: %w", filepath.Base(path), err)
		}
		if limit > 0 && info.Size() > limit {
			return out, fmt.Errorf("2075 %s is %s, exceeds server limit of %s",
				filepath.Base(path), humanSize(info.Size()), humanSize(limit))
		}

		f, err := os.Open(path)
		if err != nil {
			return out, fmt.Errorf("2074 open %s: %w", filepath.Base(path), err)
		}
		blobID, serverType, size, err := s.UploadBlob(a.ctx, accountID, f)
		f.Close()
		if err != nil {
			return out, err
		}

		out = append(out, UploadedAttachment{
			BlobID: blobID,
			Type:   attachmentType(path, serverType),
			Name:   filepath.Base(path),
			Size:   size,
		})
	}
	return out, nil
}

// attachmentType 优先按扩展名定类型。服务器通常只能回 application/octet-stream,
// 收件人的客户端据此无法预览, 甚至不知道该用什么程序打开。
func attachmentType(path, serverType string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return normalizeType(t)
	}
	if serverType != "" {
		return normalizeType(serverType)
	}
	return "application/octet-stream"
}

func humanSize(n int64) string {
	const mb = 1 << 20
	if n >= mb {
		return strconv.FormatFloat(float64(n)/mb, 'f', 1, 64) + " MB"
	}
	return strconv.FormatInt(n/1024, 10) + " KB"
}
