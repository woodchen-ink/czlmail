package smime

import (
	"crypto"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrUnsupported 表示当前系统没有可用的证书存储。
var ErrUnsupported = errors.New("smime: certificate store not supported on this platform")

// Certificate 是证书存储里一张带私钥的证书, 给界面列表用。
type Certificate struct {
	// Thumbprint 是证书 DER 的 SHA-1 (十六进制大写), 与 Windows 证书管理器里的「指纹」一致。
	Thumbprint string    `json:"thumbprint"`
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	Emails     []string  `json:"emails"`
	NotBefore  time.Time `json:"notBefore"`
	NotAfter   time.Time `json:"notAfter"`
	// Problem 非空时证书不能用来签名 (过期、不能签邮件…), 值是错误码。
	Problem string `json:"problem"`
}

// Signer 是一张能签名的证书: 证书、证书链与私钥。
type Signer struct {
	Cert  *x509.Certificate
	Chain []*x509.Certificate
	Key   crypto.Signer
	// Close 释放私钥句柄, 可以为 nil。
	Close func()
}

// Thumbprint 计算证书指纹。
func Thumbprint(c *x509.Certificate) string {
	sum := sha1.Sum(c.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

var oidEmailAddress = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}

// Emails 返回证书上的邮箱地址: SAN 里的 rfc822Name, 没有时取主题里的 E= 。
func Emails(c *x509.Certificate) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	for _, e := range c.EmailAddresses {
		add(e)
	}
	if len(out) == 0 {
		for _, n := range c.Subject.Names {
			if n.Type.Equal(oidEmailAddress) {
				if s, ok := n.Value.(string); ok {
					add(s)
				}
			}
		}
	}
	return out
}

// 证书问题的错误码, 与前端 ERROR_MESSAGES 对应。
const (
	ProblemNotYetValid = "2401"
	ProblemExpired     = "2402"
	ProblemUsage       = "2403"
	ProblemAddress     = "2404"
)

// CheckUsable 判断证书此刻能否为 from 签名; 返回问题码, 能用时为空。from 为空时不查地址。
func CheckUsable(c *x509.Certificate, from string, now time.Time) string {
	if now.Before(c.NotBefore) {
		return ProblemNotYetValid
	}
	if now.After(c.NotAfter) {
		return ProblemExpired
	}
	if c.KeyUsage != 0 && c.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
		return ProblemUsage
	}
	if len(c.ExtKeyUsage) > 0 || len(c.UnknownExtKeyUsage) > 0 {
		ok := false
		for _, u := range c.ExtKeyUsage {
			if u == x509.ExtKeyUsageEmailProtection || u == x509.ExtKeyUsageAny {
				ok = true
			}
		}
		if !ok {
			return ProblemUsage
		}
	}
	if from != "" {
		match := false
		for _, e := range Emails(c) {
			if strings.EqualFold(e, from) {
				match = true
			}
		}
		if !match {
			return ProblemAddress
		}
	}
	return ""
}

// Describe 把证书整理成界面列表的一项。
func Describe(c *x509.Certificate, now time.Time) Certificate {
	return Certificate{
		Thumbprint: Thumbprint(c),
		Subject:    displayName(c.Subject.CommonName, c.Subject.Organization),
		Issuer:     displayName(c.Issuer.CommonName, c.Issuer.Organization),
		Emails:     Emails(c),
		NotBefore:  c.NotBefore,
		NotAfter:   c.NotAfter,
		Problem:    CheckUsable(c, "", now),
	}
}

func displayName(cn string, org []string) string {
	if cn != "" {
		return cn
	}
	return strings.Join(org, ", ")
}

// chainFor 用系统的信任库补全证书链, 返回签名证书之后要附上的证书 (不含自签名的根)。
// 补不全时只附签名证书本身: 收件人多半自己有中间证书, 签名照样有效。
func chainFor(c *x509.Certificate, intermediates []*x509.Certificate) []*x509.Certificate {
	pool := x509.NewCertPool()
	for _, i := range intermediates {
		pool.AddCert(i)
	}
	chains, err := c.Verify(x509.VerifyOptions{
		Intermediates: pool,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil || len(chains) == 0 {
		return nil
	}
	var out []*x509.Certificate
	for _, x := range chains[0][1:] {
		if bytesEqual(x.RawIssuer, x.RawSubject) {
			continue
		}
		out = append(out, x)
	}
	return out
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

// Sign 用这张证书给 content 签名, 签名时间取现在。可直接作为 SignMessage 的 SignFunc。
func (s *Signer) Sign(content []byte) ([]byte, error) {
	return SignDetached(content, s.Key, s.Cert, s.Chain, time.Now())
}
