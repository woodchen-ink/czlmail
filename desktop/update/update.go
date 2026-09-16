// Package update 检查并安装新版本。
//
// 版本来源是 GitHub Releases: CI 按 tag 构建安装包并附带 SHA256SUMS。
// 下载后必须校验哈希才会运行安装包 —— 自更新是以用户权限执行一个从网上拉下来的程序,
// 没有校验就等于给中间人留了一个任意代码执行的口子。
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo 是发布版本的 GitHub 仓库。
const Repo = "woodchen-ink/czlmail"

// ChecksumAsset 是 CI 生成的哈希清单文件名, 格式同 sha256sum 输出。
const ChecksumAsset = "SHA256SUMS"

// ErrNoRelease 表示仓库还没有可用的发布(或仓库不可访问)。
var ErrNoRelease = errors.New("no published release")

// Release 是一个可用版本。
type Release struct {
	Version     string    `json:"version"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
	// AssetName / AssetURL 是本平台的安装包, 为空表示该版本没有本平台的包。
	AssetName string `json:"assetName"`
	AssetURL  string `json:"assetUrl"`
	AssetSize int64  `json:"assetSize"`
	sumsURL   string
}

var client = &http.Client{Timeout: 30 * time.Second}

// Latest 查询最新的正式发布。
func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "czlmail-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check release: %w", err)
	}
	defer resp.Body.Close()
	// 私有仓库或尚未发布时 GitHub 返回 404。
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check release: HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if body.Draft || body.Prerelease || body.TagName == "" {
		return nil, ErrNoRelease
	}

	r := &Release{Version: body.TagName, Notes: body.Body, URL: body.HTMLURL, PublishedAt: body.PublishedAt}
	want := assetName()
	for _, a := range body.Assets {
		switch {
		case a.Name == ChecksumAsset:
			r.sumsURL = a.URL
		case want != "" && a.Name == want:
			r.AssetName, r.AssetURL, r.AssetSize = a.Name, a.URL, a.Size
		}
	}
	return r, nil
}

// assetName 是本平台安装包的文件名, 与 CI 的产物命名一致。
func assetName() string {
	switch runtime.GOOS {
	case "windows":
		return "czlmail-" + runtime.GOARCH + "-installer.exe"
	case "darwin":
		return "czlmail-darwin-universal.zip"
	}
	return ""
}

// Newer 报告 latest 是否比 current 新。current 为 dev 等非版本号时不提示更新。
func Newer(latest, current string) bool {
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Download 下载本平台的安装包到临时目录并校验 SHA-256, 返回文件路径。
// progress 以已下载字节数回调, 可为 nil。
func Download(ctx context.Context, r *Release, progress func(done, total int64)) (string, error) {
	if r.AssetURL == "" {
		return "", errors.New("this release has no installer for this platform")
	}
	if r.sumsURL == "" {
		return "", errors.New("release has no SHA256SUMS; refusing to install an unverified file")
	}
	want, err := expectedSum(ctx, r.sumsURL, r.AssetName)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "czlmail-update-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, r.AssetName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "czlmail-updater")
	dl := &http.Client{} // 大文件不设总超时, 由 ctx 控制取消
	resp, err := dl.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	counter := &countingWriter{total: resp.ContentLength, progress: progress}
	_, err = io.Copy(io.MultiWriter(f, hash, counter), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("download: %w", err)
	}

	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		os.RemoveAll(dir)
		return "", fmt.Errorf("checksum mismatch for %s", r.AssetName)
	}
	return path, nil
}

func expectedSum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "czlmail-updater")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksums: HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum listed for %s", name)
}

type countingWriter struct {
	done, total int64
	progress    func(done, total int64)
	last        time.Time
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.done += int64(len(p))
	if c.progress != nil && (time.Since(c.last) > 200*time.Millisecond || c.done == c.total) {
		c.last = time.Now()
		c.progress(c.done, c.total)
	}
	return len(p), nil
}
