package notes

import "net/http"

// PreviewType trusts safe magic-byte signatures rather than stale or generic remote MIME metadata.
func PreviewType(data []byte) string {
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "application/pdf":
		return detected
	default:
		return "application/octet-stream"
	}
}
