package smime

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// toIndefinite 把前 levels 层的复合元素改写成不定长 BER, 模仿 Outlook / Apple Mail 的写法。
// OCTET STRING 拆成两段 (0x24), 检验分段合并。
func toIndefinite(t *testing.T, der []byte, levels int) []byte {
	t.Helper()
	var out []byte
	for len(der) > 0 {
		h, content := testTLV(t, der)
		tag, rest := der[0], der[h+len(content):]
		switch {
		case tag&0x20 != 0 && levels > 0:
			out = append(out, tag, 0x80)
			out = append(out, toIndefinite(t, content, levels-1)...)
			out = append(out, 0, 0)
		case tag == 0x04 && len(content) > 1 && levels > 0:
			half := len(content) / 2
			out = append(out, 0x24, 0x80)
			out = append(out, encodeDER([]byte{0x04}, content[:half])...)
			out = append(out, encodeDER([]byte{0x04}, content[half:])...)
			out = append(out, 0, 0)
		default:
			out = append(out, der[:h+len(content)]...)
		}
		der = rest
	}
	return out
}

func testTLV(t *testing.T, b []byte) (int, []byte) {
	t.Helper()
	l, i := int(b[1]), 2
	if l > 0x80 {
		n := l & 0x7f
		l = 0
		for _, c := range b[2 : 2+n] {
			l = l<<8 | int(c)
		}
		i += n
	}
	return i, b[i : i+l]
}

func TestBERToDERRoundTrip(t *testing.T) {
	_, _, leaf, key := testChain(t, "martin@example.cz")
	der, err := SignDetached([]byte("obsah"), key, leaf, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for levels := 0; levels <= 8; levels++ {
		ber := toIndefinite(t, der, levels)
		got, err := berToDER(ber)
		if err != nil {
			t.Fatalf("levels %d: %v", levels, err)
		}
		if !bytes.Equal(got, der) {
			t.Fatalf("levels %d: DER differs", levels)
		}
	}
	for _, bad := range [][]byte{{0x30, 0x80, 0x02, 0x01, 0x01}, {0x04, 0x80, 0, 0}, {0x30, 0x05, 0x02}} {
		if _, err := berToDER(bad); err == nil {
			t.Fatalf("% x: want error", bad)
		}
	}
}

// Outlook: 外层 protocol="application/x-pkcs7-signature", 签名部分 application/pkcs7-signature, BER 编码。
func TestVerifyOutlookStyle(t *testing.T) {
	roots, _, leaf, key := testChain(t, "martin@example.cz")
	msg := signedSample(t, leaf, key)

	marker := []byte("S/MIME Cryptographic Signature\r\n\r\n")
	start := bytes.Index(msg, marker) + len(marker)
	end := start + bytes.Index(msg[start:], []byte("--"))
	if start < len(marker) || end < start {
		t.Fatal("signature part not found")
	}
	der, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(msg[start:end])))
	if err != nil {
		t.Fatal(err)
	}
	ber := base64.StdEncoding.EncodeToString(toIndefinite(t, der, 4)) + "\r\n"
	var out []byte
	out = append(out, msg[:start]...)
	out = append(out, ber...)
	out = append(out, msg[end:]...)
	out = bytes.Replace(out, []byte(`protocol="application/pkcs7-signature"`), []byte(`protocol="application/x-pkcs7-signature"`), 1)

	v, err := VerifyMessage(out, "martin@example.cz", time.Now(), roots)
	if err != nil || v == nil || v.Status != StatusValid {
		t.Fatalf("outlook style: %+v %v", v, err)
	}

	// 读不懂的签名不等于"内容被改动"。
	broken := bytes.Replace(out, []byte(ber[:40]), []byte(strings.Repeat("A", 40)), 1)
	if v, _ := VerifyMessage(broken, "martin@example.cz", time.Now(), roots); v == nil || v.Status != StatusUnsupported {
		t.Fatalf("unreadable signature: %+v", v)
	}

	// 外层定长、只有里层不定长的写法也要能读。
	h, content := testTLV(t, der)
	inner := encodeDER(der[:1], toIndefinite(t, content, 4))
	if h == 0 || bytes.Equal(inner, der) || inner[1] == 0x80 {
		t.Fatal("inner BER sample not built")
	}
	var mixed []byte
	mixed = append(mixed, msg[:start]...)
	mixed = append(mixed, base64.StdEncoding.EncodeToString(inner)+"\r\n"...)
	mixed = append(mixed, msg[end:]...)
	if v, err := VerifyMessage(mixed, "martin@example.cz", time.Now(), roots); err != nil || v == nil || v.Status != StatusValid {
		t.Fatalf("inner BER: %+v %v", v, err)
	}
}
