package smime

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"mime/quotedprintable"
	"strings"
)

// SignFunc 对被签名部件的原始字节给出 DER 编码的 CMS 签名。
type SignFunc func(content []byte) ([]byte, error)

// SignMessage 把一封完整的 RFC 5322 邮件改成 multipart/signed:
// 原邮件的 Content-* 头与正文成为被签名部件, 其余头 (From、To、Subject、Date、Message-ID…) 留在外层。
//
// 被签名部件必须一字节不差地到达收件人, 所以先规范化: 换行一律 CRLF, 未编码 (7bit/8bit/binary)
// 的叶子部件改成 quoted-printable (文本) 或 base64 (其他)。这样每行都短、只有 7 位字符,
// 中途的服务器没有理由改写它 (比如去掉行尾空格或转换 8 位编码)。
func SignMessage(raw []byte, sign SignFunc) ([]byte, error) {
	raw = toCRLF(raw)
	headers, body, err := splitEntity(raw)
	if err != nil {
		return nil, err
	}
	var outer, content []string
	for _, h := range headers {
		switch name := strings.ToLower(headerName(h)); {
		case strings.HasPrefix(name, "content-"):
			content = append(content, h)
		case name == "mime-version":
		default:
			outer = append(outer, h)
		}
	}
	if len(content) == 0 {
		content = []string{"Content-Type: text/plain; charset=us-ascii"}
	}
	inner, err := normalizeEntity(content, body, 0)
	if err != nil {
		return nil, err
	}
	signature, err := sign(inner)
	if err != nil {
		return nil, err
	}

	var rnd [12]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	boundary := "----=_smime_" + hex.EncodeToString(rnd[:])

	var out bytes.Buffer
	for _, h := range outer {
		out.WriteString(h + "\r\n")
	}
	out.WriteString("MIME-Version: 1.0\r\n")
	out.WriteString(`Content-Type: multipart/signed; protocol="application/pkcs7-signature"; micalg=sha-256;` + "\r\n" + ` boundary="` + boundary + `"` + "\r\n")
	out.WriteString("\r\nThis is a cryptographically signed message in MIME format.\r\n\r\n")
	out.WriteString("--" + boundary + "\r\n")
	out.Write(inner)
	out.WriteString("\r\n--" + boundary + "\r\n")
	out.WriteString(`Content-Type: application/pkcs7-signature; name="smime.p7s"` + "\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString(`Content-Disposition: attachment; filename="smime.p7s"` + "\r\n")
	out.WriteString("Content-Description: S/MIME Cryptographic Signature\r\n\r\n")
	out.WriteString(base64Lines(signature))
	out.WriteString("--" + boundary + "--\r\n")
	return out.Bytes(), nil
}

// toCRLF 把单独的 LF 或 CR 都换成 CRLF。
func toCRLF(b []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(b) + len(b)/40)
	for i := 0; i < len(b); i++ {
		switch c := b[i]; c {
		case '\r':
			out.WriteString("\r\n")
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
		case '\n':
			out.WriteString("\r\n")
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// splitEntity 拆出头 (折行合并成一项, 保留原样的 CRLF 与缩进) 与正文。
func splitEntity(raw []byte) ([]string, []byte, error) {
	var head, body []byte
	switch {
	case bytes.HasPrefix(raw, []byte("\r\n")):
		head, body = nil, raw[2:]
	default:
		i := bytes.Index(raw, []byte("\r\n\r\n"))
		if i < 0 {
			return nil, nil, errors.New("smime: message has no body separator")
		}
		head, body = raw[:i], raw[i+4:]
	}
	var headers []string
	if len(head) > 0 {
		for _, line := range strings.Split(string(head), "\r\n") {
			if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(headers) > 0 {
				headers[len(headers)-1] += "\r\n" + line
				continue
			}
			headers = append(headers, line)
		}
	}
	return headers, body, nil
}

func headerName(h string) string {
	if i := strings.IndexByte(h, ':'); i > 0 {
		return strings.TrimSpace(h[:i])
	}
	return ""
}

func headerValue(headers []string, name string) string {
	for _, h := range headers {
		if strings.EqualFold(headerName(h), name) {
			v := h[strings.IndexByte(h, ':')+1:]
			v = strings.NewReplacer("\r\n ", " ", "\r\n\t", " ").Replace(v)
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func withHeader(headers []string, name, value string) []string {
	out := make([]string, 0, len(headers)+1)
	for _, h := range headers {
		if !strings.EqualFold(headerName(h), name) {
			out = append(out, h)
		}
	}
	return append(out, name+": "+value)
}

const maxDepth = 20

// normalizeEntity 返回 headers + body 规范化后的 MIME 部件 (头、空行、正文)。
func normalizeEntity(headers []string, body []byte, depth int) ([]byte, error) {
	if depth > maxDepth {
		return nil, errors.New("smime: MIME structure too deep")
	}
	mediaType, params, err := mime.ParseMediaType(headerValue(headers, "Content-Type"))
	if err != nil {
		mediaType, params = "text/plain", nil
	}
	cte := strings.ToLower(headerValue(headers, "Content-Transfer-Encoding"))

	if strings.HasPrefix(mediaType, "multipart/") && params["boundary"] != "" {
		parts, preamble, epilogue, err := splitMultipart(body, params["boundary"])
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		if len(preamble) > 0 {
			out.Write(preamble)
			out.WriteString("\r\n")
		}
		for _, p := range parts {
			h, b, err := splitEntity(p)
			if err != nil {
				return nil, err
			}
			n, err := normalizeEntity(h, b, depth+1)
			if err != nil {
				return nil, err
			}
			out.WriteString("--" + params["boundary"] + "\r\n")
			out.Write(n)
			out.WriteString("\r\n")
		}
		out.WriteString("--" + params["boundary"] + "--\r\n")
		out.Write(epilogue)
		return join(headers, out.Bytes()), nil
	}

	switch cte {
	case "base64", "quoted-printable":
		return join(headers, body), nil
	}
	if strings.HasPrefix(mediaType, "message/") && is7bitSafe(body) {
		// message/* 不允许 base64 与 quoted-printable (RFC 2046 5.2), 能原样过就原样。
		return join(headers, body), nil
	}
	if strings.HasPrefix(mediaType, "text/") {
		var qp bytes.Buffer
		w := quotedprintable.NewWriter(&qp)
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return join(withHeader(headers, "Content-Transfer-Encoding", "quoted-printable"), toCRLF(qp.Bytes())), nil
	}
	return join(withHeader(headers, "Content-Transfer-Encoding", "base64"), []byte(base64Lines(body))), nil
}

func join(headers []string, body []byte) []byte {
	var out bytes.Buffer
	for _, h := range headers {
		out.WriteString(h + "\r\n")
	}
	out.WriteString("\r\n")
	out.Write(body)
	return out.Bytes()
}

// splitMultipart 按 RFC 2046 5.1.1 拆分: 分隔线前的 CRLF 属于分隔线。
func splitMultipart(body []byte, boundary string) (parts [][]byte, preamble, epilogue []byte, err error) {
	delim := []byte("--" + boundary)
	// 找出所有位于行首的分隔线。
	var starts []int
	for i := 0; i <= len(body)-len(delim); {
		j := bytes.Index(body[i:], delim)
		if j < 0 {
			break
		}
		at := i + j
		if at == 0 || (at >= 2 && body[at-2] == '\r' && body[at-1] == '\n') {
			starts = append(starts, at)
		}
		i = at + len(delim)
	}
	if len(starts) == 0 {
		return nil, nil, nil, errors.New("smime: multipart boundary not found")
	}
	if starts[0] >= 2 {
		preamble = body[:starts[0]-2]
	}
	for k, at := range starts {
		rest := body[at+len(delim):]
		if bytes.HasPrefix(rest, []byte("--")) {
			// 结束分隔线; 之后是 epilogue (跳过分隔线所在行的剩余部分)。
			if e := bytes.Index(rest, []byte("\r\n")); e >= 0 {
				epilogue = rest[e+2:]
			}
			return parts, preamble, epilogue, nil
		}
		lineEnd := bytes.Index(rest, []byte("\r\n"))
		if lineEnd < 0 {
			break
		}
		partStart := at + len(delim) + lineEnd + 2
		partEnd := len(body)
		if k+1 < len(starts) {
			partEnd = starts[k+1] - 2
		}
		if partEnd < partStart {
			partEnd = partStart
		}
		parts = append(parts, body[partStart:partEnd])
	}
	return nil, nil, nil, errors.New("smime: multipart has no closing boundary")
}

func is7bitSafe(b []byte) bool {
	lineLen := 0
	for _, c := range b {
		if c >= 0x80 || c == 0 {
			return false
		}
		if c == '\n' {
			lineLen = 0
			continue
		}
		lineLen++
		if lineLen > 998 {
			return false
		}
	}
	return true
}

func base64Lines(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var out strings.Builder
	for len(enc) > 76 {
		out.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	if enc != "" {
		out.WriteString(enc + "\r\n")
	}
	return out.String()
}
