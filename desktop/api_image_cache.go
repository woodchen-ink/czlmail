package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/imgcache"
	"github.com/woodchen-ink/czlmail/desktop/internal/safehttp"
)

// 正文图片缓存: 远程图片经本地路径代理下载并落盘, 内嵌(cid)图片的预览也存一份,
// 再次打开邮件时不必重新下载。容量 500MB, 按最近使用淘汰, 设置里可清除。

// RemoteImagePath 是远程图片的本地代理路径, 形如 /czl-remote?url=https%3A%2F%2F...。
const RemoteImagePath = "/czl-remote"

// maxRemoteImageBytes 限制单张远程图片的大小。
const maxRemoteImageBytes = 15 << 20

type imageCache struct {
	once   sync.Once
	cache  *imgcache.Cache
	client *http.Client
}

// imageStore 返回缓存; 定位不到缓存目录时为 nil, 此时照常下载只是不缓存。
func (a *App) imageStore() (*imgcache.Cache, *http.Client) {
	a.images.once.Do(func() {
		a.images.client = safehttp.NewClient(20 * time.Second)
		base, err := os.UserCacheDir()
		if err != nil {
			a.log.Warn("image cache disabled", "err", err)
			return
		}
		a.images.cache = imgcache.New(filepath.Join(base, config.DirName(), "images"), imgcache.DefaultMaxBytes)
	})
	return a.images.cache, a.images.client
}

// serveRemoteImage 代理邮件正文里的远程图片。
//
// 只有用户已允许加载图片的邮件才会改写到这里(屏蔽状态下 src 在清洗时就拆掉了)。
// 与直连相比: 不带 Referer 与 Cookie, 只连公网地址, 只返回嗅探得出的图片。
func (a *App) serveRemoteImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		http.NotFound(w, r)
		return
	}
	cache, client := a.imageStore()
	key := "remote:" + u.String()
	if cache != nil {
		if data, typ, ok := cache.Get(key); ok {
			writeCachedImage(w, typ, data)
			return
		}
	}

	data, typ, err := fetchRemoteImage(r.Context(), client, u.String())
	if err != nil {
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	if typ == "" {
		http.NotFound(w, r)
		return
	}
	if cache != nil {
		if err := cache.Put(key, typ, data); err != nil {
			a.log.Warn("cache remote image", "err", err)
		}
	}
	writeCachedImage(w, typ, data)
}

func fetchRemoteImage(ctx context.Context, client *http.Client, target string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	// 有的图床拒绝没有 UA 的请求。
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CZL Mail)")
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxRemoteImageBytes {
		return nil, "", fmt.Errorf("image too large")
	}
	return data, imgcache.ImageType(data, resp.Header.Get("Content-Type")), nil
}

func writeCachedImage(w http.ResponseWriter, typ string, data []byte) {
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

// cachedBlob 取内嵌图片的预览: 先查缓存, 没有再下载并存起来。blob 内容不可变, 不用过期。
func (a *App) cachedBlob(ctx context.Context, accountID, blobID string) ([]byte, error) {
	cache, _ := a.imageStore()
	key := "blob:" + accountID + "/" + blobID
	if cache != nil {
		if data, _, ok := cache.Get(key); ok {
			return data, nil
		}
	}
	s, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	rc, err := s.DownloadBlob(ctx, accountID, blobID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, maxPreviewBytes)); err != nil {
		return nil, err
	}
	if cache != nil {
		// 类型由请求方给出, 缓存里的类型只作记录。
		if err := cache.Put(key, "application/octet-stream", buf.Bytes()); err != nil {
			a.log.Warn("cache blob", "err", err)
		}
	}
	return buf.Bytes(), nil
}

// GetImageCacheSize 返回图片缓存占用的字节数。
func (a *App) GetImageCacheSize() int64 {
	cache, _ := a.imageStore()
	if cache == nil {
		return 0
	}
	return cache.Size()
}

// ClearImageCache 清除图片缓存。
func (a *App) ClearImageCache() error {
	cache, _ := a.imageStore()
	if cache == nil {
		return nil
	}
	if err := cache.Clear(); err != nil {
		return fmt.Errorf("2240 clear image cache: %w", err)
	}
	return nil
}
