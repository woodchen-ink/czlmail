// Package smime 给待发邮件加 S/MIME 签名 (RFC 8551): 分离式 multipart/signed,
// 签名是 CMS SignedData (RFC 5652), SHA-256, 附带签名证书及其证书链。
//
// 只做签名, 不做加密。私钥不经过本包: 调用方给一个 crypto.Signer
// (Windows 上是证书存储里的密钥, 私钥不离开系统)。
package smime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

var (
	oidData            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidContentType     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidSHA256          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
)

// --- DER, 手写: encoding/asn1 不会按 DER 要求给 SET OF 排序, 也不方便写 IMPLICIT [0] ---

func derLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var b []byte
	for ; n > 0; n >>= 8 {
		b = append([]byte{byte(n)}, b...)
	}
	return append([]byte{0x80 | byte(len(b))}, b...)
}

func tlv(tag byte, content ...[]byte) []byte {
	body := bytes.Join(content, nil)
	out := append([]byte{tag}, derLength(len(body))...)
	return append(out, body...)
}

func sequence(content ...[]byte) []byte { return tlv(0x30, content...) }

// setOf 按编码排序元素 (X.690 11.6)。
func setOf(elems ...[]byte) []byte {
	sorted := append([][]byte(nil), elems...)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	return tlv(0x31, sorted...)
}

func mustMarshal(v any) []byte {
	b, err := asn1.Marshal(v)
	if err != nil {
		panic(err) // 只用于 OID、整数、时间这类不会失败的值
	}
	return b
}

func oid(o asn1.ObjectIdentifier) []byte { return mustMarshal(o) }

var derNull = []byte{0x05, 0x00}

// signingTime 在 2050 年前用 UTCTime, 之后用 GeneralizedTime (RFC 5652 11.3)。
func signingTime(t time.Time) []byte {
	t = t.UTC()
	if t.Year() < 2050 {
		return tlv(0x17, []byte(t.Format("060102150405Z")))
	}
	return tlv(0x18, []byte(t.Format("20060102150405Z")))
}

func attribute(typ asn1.ObjectIdentifier, value []byte) []byte {
	return sequence(oid(typ), setOf(value))
}

// SignDetached 返回 content 的分离式签名: DER 编码的 ContentInfo(SignedData)。
// chain 是签名证书之外随签名附上的证书 (中间证书), 让收件人不必自己去找。
func SignDetached(content []byte, key crypto.Signer, cert *x509.Certificate, chain []*x509.Certificate, now time.Time) ([]byte, error) {
	if key == nil || cert == nil {
		return nil, errors.New("smime: no key or certificate")
	}
	var sigAlg []byte
	var opts crypto.SignerOpts = crypto.SHA256
	switch pub := key.Public().(type) {
	case *rsa.PublicKey:
		sigAlg = sequence(oid(oidRSAEncryption), derNull)
		if c, ok := cert.PublicKey.(*rsa.PublicKey); !ok || !c.Equal(pub) {
			return nil, errors.New("smime: key does not match certificate")
		}
	case *ecdsa.PublicKey:
		sigAlg = sequence(oid(oidECDSAWithSHA256))
		if c, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !c.Equal(pub) {
			return nil, errors.New("smime: key does not match certificate")
		}
	default:
		return nil, fmt.Errorf("smime: unsupported key type %T", pub)
	}

	sha := sequence(oid(oidSHA256), derNull)
	digest := sha256.Sum256(content)
	signedAttrs := setOf(
		attribute(oidContentType, oid(oidData)),
		attribute(oidSigningTime, signingTime(now)),
		attribute(oidMessageDigest, tlv(0x04, digest[:])),
	)
	// 签的是按 SET OF (0x31) 编码的属性, 放进 SignerInfo 时换成 [0] IMPLICIT。
	attrsHash := sha256.Sum256(signedAttrs)
	signature, err := key.Sign(rand.Reader, attrsHash[:], opts)
	if err != nil {
		return nil, fmt.Errorf("smime: sign: %w", err)
	}

	issuerAndSerial := sequence(cert.RawIssuer, mustMarshal(new(big.Int).Set(cert.SerialNumber)))
	signerInfo := sequence(
		mustMarshal(1),
		issuerAndSerial,
		sha,
		append([]byte{0xa0}, signedAttrs[1:]...),
		sigAlg,
		tlv(0x04, signature),
	)
	certs := [][]byte{cert.Raw}
	for _, c := range chain {
		certs = append(certs, c.Raw)
	}
	signedData := sequence(
		mustMarshal(1),
		setOf(sha),
		sequence(oid(oidData)), // 分离式: 没有 eContent
		tlv(0xa0, certs...),
		setOf(signerInfo),
	)
	return sequence(oid(oidSignedData), tlv(0xa0, signedData)), nil
}
