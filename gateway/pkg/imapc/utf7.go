package imapc

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Modified UTF-7 (RFC 3501 §5.1.3) for mailbox names.

var b64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,").WithPadding(base64.NoPadding)

// EncodeMailbox encodes a UTF-8 mailbox name for the wire.
func EncodeMailbox(s string) string {
	var sb strings.Builder
	var pending []rune
	flush := func() {
		if len(pending) == 0 {
			return
		}
		u := utf16.Encode(pending)
		b := make([]byte, 0, len(u)*2)
		for _, x := range u {
			b = append(b, byte(x>>8), byte(x))
		}
		sb.WriteByte('&')
		sb.WriteString(b64.EncodeToString(b))
		sb.WriteByte('-')
		pending = pending[:0]
	}
	for _, r := range s {
		if r >= 0x20 && r <= 0x7e {
			flush()
			if r == '&' {
				sb.WriteString("&-")
			} else {
				sb.WriteRune(r)
			}
			continue
		}
		pending = append(pending, r)
	}
	flush()
	return sb.String()
}

// DecodeMailbox decodes a wire mailbox name; malformed input is returned unchanged.
func DecodeMailbox(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			sb.WriteByte(s[i])
			continue
		}
		end := strings.IndexByte(s[i:], '-')
		if end < 0 {
			return s
		}
		chunk := s[i+1 : i+end]
		i += end
		if chunk == "" {
			sb.WriteByte('&')
			continue
		}
		b, err := b64.DecodeString(chunk)
		if err != nil || len(b)%2 != 0 {
			return s
		}
		u := make([]uint16, len(b)/2)
		for j := range u {
			u[j] = uint16(b[2*j])<<8 | uint16(b[2*j+1])
		}
		for _, r := range utf16.Decode(u) {
			if r == utf8.RuneError {
				return s
			}
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
