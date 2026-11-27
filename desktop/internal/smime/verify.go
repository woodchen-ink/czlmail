package smime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	_ "crypto/sha1" // 旧客户端仍用 SHA-1 签名, 只用于核对, 不用于签名
	_ "crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"mime"
	"strings"
	"time"
)

// 验证结果。
const (
	StatusValid         = "valid"     // 签名有效, 证书可信, 地址相符
	StatusInvalid       = "invalid"   // 签名对不上: 签名后内容被改动 (只用于摘要或签名不符, 读不懂的格式算 unsupported)
	StatusUntrusted     = "untrusted" // 签名对得上, 但证书链不可信 (或不能用于邮件)
	StatusSenderUnknown = "sender_unknown"
	StatusAddress       = "address"     // 签名有效, 但证书上没有发件地址
	StatusUnsupported   = "unsupported" // 签名格式不支持 (不透明签名、RSA-PSS…)
)

// Verification 是一封签名邮件的核对结果。
type Verification struct {
	Status string `json:"status"`
	// Signer 是签名证书, 解析不出证书时为 nil。
	Signer *Certificate `json:"signer"`
	// SignedAt 是签名时间 (签名者自己声明的), 没有时为零值。
	SignedAt time.Time `json:"signedAt"`
	// Expired 表示证书现在已过期。
	Expired bool `json:"expired"`
	// Detail 是给技术人员看的原因 (英文), 界面按 Status 显示本地化文字。
	Detail string `json:"detail"`
}

var (
	oidSHA1      = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA384    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512    = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidECPublic  = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidRSAPSS    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidSignedRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1} // 前缀: sha*WithRSAEncryption
	oidSignedEC  = asn1.ObjectIdentifier{1, 2, 840, 10045, 4}     // 前缀: ecdsa-with-*
)

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type cmsContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type cmsSignedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContentInfo asn1.RawValue
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos      asn1.RawValue
}

type cmsSignerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    algorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgorithm algorithmIdentifier
	Signature          []byte
	UnsignedAttrs      asn1.RawValue `asn1:"optional,tag:1"`
}

type issuerAndSerialNumber struct {
	Issuer asn1.RawValue
	Serial asn1.RawValue
}

type cmsAttribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// VerifyMessage 核对一封 RFC 5322 邮件的 S/MIME 签名。不是签名邮件时返回 nil, nil。
// roots 为 nil 时用系统信任的根证书 (Windows 上即 Outlook 用的那一套)。
func VerifyMessage(raw []byte, from string, now time.Time, roots *x509.CertPool) (*Verification, error) {
	raw = toCRLF(raw)
	headers, body, err := splitEntity(raw)
	if err != nil {
		return nil, nil
	}
	mediaType, params, err := mime.ParseMediaType(headerValue(headers, "Content-Type"))
	if err != nil {
		return nil, nil
	}
	switch mediaType {
	case "multipart/signed":
		proto := strings.ToLower(params["protocol"])
		if proto != "application/pkcs7-signature" && proto != "application/x-pkcs7-signature" {
			return nil, nil
		}
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		if strings.EqualFold(params["smime-type"], "signed-data") {
			return &Verification{Status: StatusUnsupported, Detail: "opaque signed-data"}, nil
		}
		return nil, nil // 加密邮件不在这里处理
	default:
		return nil, nil
	}

	parts, _, _, err := splitMultipart(body, params["boundary"])
	if err != nil || len(parts) != 2 {
		return &Verification{Status: StatusUnsupported, Detail: "malformed multipart/signed"}, nil
	}
	content := parts[0]
	sh, sb, err := splitEntity(parts[1])
	if err != nil {
		return &Verification{Status: StatusUnsupported, Detail: "malformed signature part"}, nil
	}
	sigType, _, typeErr := mime.ParseMediaType(headerValue(sh, "Content-Type"))
	// Outlook 在外层写 x-pkcs7-signature, 签名部分却是 pkcs7-signature; 两者是同一种格式。
	if typeErr != nil || !samePKCS7Type(sigType, params["protocol"]) {
		return &Verification{Status: StatusUnsupported, Detail: "unexpected signature part type " + sigType}, nil
	}
	der := sb
	if strings.EqualFold(headerValue(sh, "Content-Transfer-Encoding"), "base64") {
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(sb))
		if der, err = base64.StdEncoding.DecodeString(clean); err != nil {
			return &Verification{Status: StatusUnsupported, Detail: "signature is not base64"}, nil
		}
	}
	// Outlook、Apple Mail 用不定长的 BER 编码; 不定长也可能只出现在里层, 所以每次都转。
	// 本来就是 DER 的原样返回; 转不了的交给 verifyDetached 解析, 读不懂时同样是 unsupported。
	if d, err := berToDER(der); err == nil {
		der = d
	}
	return verifyDetached(content, der, from, now, roots), nil
}

func verifyDetached(content, der []byte, from string, now time.Time, roots *x509.CertPool) *Verification {
	bad := func(status, detail string) *Verification { return &Verification{Status: status, Detail: detail} }

	var ci cmsContentInfo
	if _, err := asn1.Unmarshal(der, &ci); err != nil || !ci.ContentType.Equal(oidSignedData) {
		return bad(StatusUnsupported, "not a CMS SignedData")
	}
	var sd cmsSignedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return bad(StatusUnsupported, "malformed SignedData: "+err.Error())
	}
	certs, err := x509.ParseCertificates(sd.Certificates.Bytes)
	if err != nil {
		return bad(StatusUnsupported, "malformed certificates: "+err.Error())
	}
	var infos []cmsSignerInfo
	for rest := sd.SignerInfos.Bytes; len(rest) > 0; {
		var si cmsSignerInfo
		if rest, err = asn1.Unmarshal(rest, &si); err != nil {
			return bad(StatusUnsupported, "malformed SignerInfo: "+err.Error())
		}
		infos = append(infos, si)
	}
	if len(infos) == 0 {
		return bad(StatusUnsupported, "no signer")
	}
	si := infos[0]

	signer := findSigner(certs, si.SID)
	if signer == nil {
		return bad(StatusUnsupported, "signer certificate not included")
	}
	desc := Describe(signer, now)
	result := &Verification{Signer: &desc, Expired: now.After(signer.NotAfter)}

	hash, ok := hashFor(si.DigestAlgorithm.Algorithm)
	if !ok {
		result.Status, result.Detail = StatusUnsupported, "digest "+si.DigestAlgorithm.Algorithm.String()
		return result
	}
	signed := content
	if len(si.SignedAttrs.FullBytes) > 0 {
		// 签名覆盖的是以 SET OF 标签编码的属性。
		signed = append([]byte{0x31}, si.SignedAttrs.FullBytes[1:]...)
		var digest []byte
		for rest := si.SignedAttrs.Bytes; len(rest) > 0; {
			var a cmsAttribute
			if rest, err = asn1.Unmarshal(rest, &a); err != nil {
				result.Status, result.Detail = StatusUnsupported, "malformed attributes"
				return result
			}
			switch {
			case a.Type.Equal(oidMessageDigest):
				_, _ = asn1.Unmarshal(a.Values.Bytes, &digest)
			case a.Type.Equal(oidSigningTime):
				var t time.Time
				if _, err := asn1.Unmarshal(a.Values.Bytes, &t); err == nil {
					result.SignedAt = t
				}
			}
		}
		h := hash.New()
		h.Write(content)
		if digest == nil || !bytes.Equal(digest, h.Sum(nil)) {
			result.Status, result.Detail = StatusInvalid, "message digest does not match: content changed after signing"
			return result
		}
	}
	h := hash.New()
	h.Write(signed)
	if err := checkSignature(signer, si.SignatureAlgorithm.Algorithm, hash, h.Sum(nil), si.Signature); err != nil {
		if errors.Is(err, errUnsupportedAlg) {
			result.Status, result.Detail = StatusUnsupported, err.Error()
		} else {
			result.Status, result.Detail = StatusInvalid, err.Error()
		}
		return result
	}

	pool := x509.NewCertPool()
	for _, c := range certs {
		if c != signer {
			pool.AddCert(c)
		}
	}
	if _, err := signer.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: pool,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}); err != nil {
		result.Status, result.Detail = StatusUntrusted, err.Error()
		return result
	}
	if strings.TrimSpace(from) == "" {
		result.Status, result.Detail = StatusSenderUnknown, "no unambiguous sender"
		return result
	}
	{
		match := false
		for _, e := range Emails(signer) {
			match = match || strings.EqualFold(e, from)
		}
		if !match {
			result.Status, result.Detail = StatusAddress, "certificate does not contain "+from
			return result
		}
	}
	result.Status = StatusValid
	return result
}

// samePKCS7Type: application/pkcs7-signature 与旧名 application/x-pkcs7-signature 视为相同。
func samePKCS7Type(a, b string) bool {
	norm := func(s string) string { return strings.Replace(strings.ToLower(s), "/x-", "/", 1) }
	return norm(a) == norm(b)
}

func findSigner(certs []*x509.Certificate, sid asn1.RawValue) *x509.Certificate {
	if sid.Class == asn1.ClassContextSpecific && sid.Tag == 0 {
		for _, c := range certs {
			if bytes.Equal(c.SubjectKeyId, sid.Bytes) {
				return c
			}
		}
		return nil
	}
	var ias issuerAndSerialNumber
	if _, err := asn1.Unmarshal(sid.FullBytes, &ias); err != nil {
		return nil
	}
	for _, c := range certs {
		serial, err := asn1.Marshal(c.SerialNumber)
		if err == nil && bytes.Equal(c.RawIssuer, ias.Issuer.FullBytes) && bytes.Equal(serial, ias.Serial.FullBytes) {
			return c
		}
	}
	return nil
}

func hashFor(o asn1.ObjectIdentifier) (crypto.Hash, bool) {
	switch {
	case o.Equal(oidSHA256):
		return crypto.SHA256, true
	case o.Equal(oidSHA1):
		return crypto.SHA1, true
	case o.Equal(oidSHA384):
		return crypto.SHA384, true
	case o.Equal(oidSHA512):
		return crypto.SHA512, true
	}
	return 0, false
}

var errUnsupportedAlg = errors.New("unsupported signature algorithm")

func hasPrefix(o, prefix asn1.ObjectIdentifier) bool {
	return len(o) > len(prefix) && o[:len(prefix)].Equal(prefix)
}

func checkSignature(c *x509.Certificate, alg asn1.ObjectIdentifier, hash crypto.Hash, digest, sig []byte) error {
	switch pub := c.PublicKey.(type) {
	case *rsa.PublicKey:
		if alg.Equal(oidRSAPSS) {
			return errUnsupportedAlg
		}
		if !alg.Equal(oidRSAEncryption) && !hasPrefix(alg, oidSignedRSA) {
			return errUnsupportedAlg
		}
		if err := rsa.VerifyPKCS1v15(pub, hash, digest, sig); err != nil {
			return errors.New("signature does not match")
		}
		return nil
	case *ecdsa.PublicKey:
		if !alg.Equal(oidECPublic) && !hasPrefix(alg, oidSignedEC) {
			return errUnsupportedAlg
		}
		if !ecdsa.VerifyASN1(pub, digest, sig) {
			return errors.New("signature does not match")
		}
		return nil
	}
	return errUnsupportedAlg
}
