package imgcache

import (
	"bytes"
	"net/http"
	"strings"
)

// ImageType 按内容判定图片类型, 不信任服务器声明; 不是图片时返回空串。
//
// 代理路径与应用同源, 把 HTML 当图片返回等于让它在应用源里渲染, 所以只放行嗅探得出的图片。
func ImageType(data []byte, declared string) string {
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon", "image/avif":
		return sniffed
	}
	// DetectContentType 不一定认得 AVIF: ftyp 盒子里的品牌是 avif / avis。
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) &&
		(bytes.Equal(data[8:12], []byte("avif")) || bytes.Equal(data[8:12], []byte("avis"))) {
		return "image/avif"
	}
	// SVG 嗅探出来是文本, 只在服务器也声明为 SVG 时接受; 响应带 CSP sandbox, 直接打开也不执行脚本。
	if strings.HasPrefix(strings.ToLower(declared), "image/svg+xml") &&
		strings.Contains(strings.ToLower(string(data[:min(len(data), 1024)])), "<svg") {
		return "image/svg+xml"
	}
	return ""
}
