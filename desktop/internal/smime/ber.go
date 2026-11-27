package smime

import "errors"

// Outlook 和 Apple Mail 把签名 (SignedData) 编成不定长的 BER (30 80 … 00 00),
// encoding/asn1 只认 DER。berToDER 把不定长改成定长, 把分段的 OCTET STRING 合成一段。
// 本来就是 DER 的部分 (证书、签名属性) 逐字节原样保留, 它们的签名与摘要不受影响。

var errBER = errors.New("malformed BER")

const maxBERDepth = 64

func berToDER(b []byte) ([]byte, error) {
	out, rest, _, err := berElement(b, 0)
	if err != nil {
		return nil, err
	}
	// 签名后面只允许补零。
	for _, c := range rest {
		if c != 0 {
			return nil, errBER
		}
	}
	return out, nil
}

// berElement 转换 b 开头的一个元素。changed 表示输出与输入不同。
func berElement(b []byte, depth int) (out, rest []byte, changed bool, err error) {
	if depth > maxBERDepth || len(b) < 2 {
		return nil, nil, false, errBER
	}
	i := 1
	if b[0]&0x1f == 0x1f { // 多字节标签
		for {
			if i >= len(b) {
				return nil, nil, false, errBER
			}
			i++
			if b[i-1]&0x80 == 0 {
				break
			}
		}
	}
	tag := b[:i]
	constructed := b[0]&0x20 != 0
	if i >= len(b) {
		return nil, nil, false, errBER
	}
	l := int(b[i])
	i++
	indefinite := l == 0x80
	switch {
	case indefinite && !constructed:
		return nil, nil, false, errBER
	case l > 0x80:
		n := l & 0x7f
		if n > 4 || i+n > len(b) {
			return nil, nil, false, errBER
		}
		l = 0
		for _, c := range b[i : i+n] {
			l = l<<8 | int(c)
		}
		i += n
	}

	var body, after []byte
	if indefinite {
		body = b[i:]
	} else {
		if l < 0 || i+l > len(b) {
			return nil, nil, false, errBER
		}
		body, after = b[i:i+l], b[i+l:]
		if !constructed {
			return b[:i+l], after, false, nil
		}
	}

	var children [][]byte
	anyChanged := false
	for {
		if indefinite {
			if len(body) < 2 {
				return nil, nil, false, errBER
			}
			if body[0] == 0 && body[1] == 0 {
				after = body[2:]
				break
			}
		} else if len(body) == 0 {
			break
		}
		child, r, ch, err := berElement(body, depth+1)
		if err != nil {
			return nil, nil, false, err
		}
		children = append(children, child)
		anyChanged = anyChanged || ch
		body = r
	}

	if b[0] == 0x24 { // 分段的 OCTET STRING: DER 里必须是一段 (0x04)
		var content []byte
		for _, c := range children {
			payload, ok := derContent(c)
			if !ok || c[0] != 0x04 {
				return nil, nil, false, errBER
			}
			content = append(content, payload...)
		}
		return encodeDER([]byte{0x04}, content), after, true, nil
	}
	if !indefinite && !anyChanged {
		return b[:len(b)-len(after)], after, false, nil
	}
	var content []byte
	for _, c := range children {
		content = append(content, c...)
	}
	return encodeDER(tag, content), after, true, nil
}

// derContent 返回一个 DER 元素 (单字节标签, 定长) 的内容。
func derContent(b []byte) ([]byte, bool) {
	if len(b) < 2 || b[0]&0x1f == 0x1f || b[1] == 0x80 {
		return nil, false
	}
	l, i := int(b[1]), 2
	if l > 0x80 {
		n := l & 0x7f
		if n > 4 || 2+n > len(b) {
			return nil, false
		}
		l = 0
		for _, c := range b[2 : 2+n] {
			l = l<<8 | int(c)
		}
		i += n
	}
	if i+l != len(b) {
		return nil, false
	}
	return b[i:], true
}

func encodeDER(tag, content []byte) []byte {
	out := append([]byte{}, tag...)
	n := len(content)
	if n < 0x80 {
		out = append(out, byte(n))
	} else {
		var lb []byte
		for v := n; v > 0; v >>= 8 {
			lb = append([]byte{byte(v)}, lb...)
		}
		out = append(out, 0x80|byte(len(lb)))
		out = append(out, lb...)
	}
	return append(out, content...)
}
