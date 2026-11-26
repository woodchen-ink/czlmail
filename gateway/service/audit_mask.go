package service

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	maskedValue    = "***"
	maxParamsBytes = 2048
)

// secretKeyWords mark a JSON field as secret when its name contains one of them.
var secretKeyWords = []string{"password", "secret", "token", "key", "credential", "authorization"}

// isSecretKey decides by name. "key" alone is too broad (setting keys, keyword), so it
// only matches as "key", "*_key", "*apikey" and "*privatekey".
func isSecretKey(name string) bool {
	n := strings.ToLower(name)
	for _, w := range secretKeyWords {
		if w == "key" {
			if n == "key" || strings.HasSuffix(n, "_key") || strings.HasSuffix(n, "apikey") || strings.HasSuffix(n, "privatekey") {
				return true
			}
			continue
		}
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// MaskParams turns a JSON request body into the audit summary: values of secret fields
// become "***" at any depth, and the result is cut to 2 KiB. A body that is not JSON
// is recorded only by size.
func MaskParams(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return `{"_unparsed_bytes":` + strconv.Itoa(len(body)) + `}`
	}
	out, _ := json.Marshal(maskValue(v))
	if len(out) > maxParamsBytes {
		return string(out[:maxParamsBytes]) + "…"
	}
	return string(out)
}

func maskValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSecretKey(k) {
				if val != nil && val != "" {
					t[k] = maskedValue
				}
				continue
			}
			t[k] = maskValue(val)
		}
	case []any:
		for i := range t {
			t[i] = maskValue(t[i])
		}
	}
	return v
}
