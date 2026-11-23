package smime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func testCert(t *testing.T, key crypto.Signer, email string, eku []x509.ExtKeyUsage, notAfter time.Time) *x509.Certificate {
	t.Helper()
	tpl := &x509.Certificate{
		SerialNumber:   big.NewInt(424242),
		Subject:        pkix.Name{CommonName: "Test Signer", Organization: []string{"CZL"}},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       notAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:    eku,
		EmailAddresses: []string{email},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// 解析 CMS 的最小结构, 只为测试核对签名。
type contentInfo struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue
}
type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContent     struct{ Type asn1.ObjectIdentifier }
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      []signerInfo  `asn1:"set"`
}
type signerInfo struct {
	Version         int
	IssuerAndSerial struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}
	DigestAlgorithm    asn1.RawValue
	SignedAttrs        asn1.RawValue `asn1:"tag:0"`
	SignatureAlgorithm asn1.RawValue
	Signature          []byte
}
type attr struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

func verifyCMS(t *testing.T, sig, content []byte, cert *x509.Certificate) {
	t.Helper()
	var ci contentInfo
	if _, err := asn1.Unmarshal(sig, &ci); err != nil {
		t.Fatalf("contentInfo: %v", err)
	}
	if !ci.Type.Equal(oidSignedData) {
		t.Fatalf("type %v", ci.Type)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		t.Fatalf("signedData: %v", err)
	}
	if len(sd.SignerInfos) != 1 {
		t.Fatalf("signers %d", len(sd.SignerInfos))
	}
	si := sd.SignerInfos[0]
	if si.IssuerAndSerial.Serial.Cmp(cert.SerialNumber) != 0 || !bytes.Equal(si.IssuerAndSerial.Issuer.FullBytes, cert.RawIssuer) {
		t.Fatal("issuerAndSerial does not identify the certificate")
	}
	// 签名覆盖的是以 SET OF 标签编码的属性。
	signed := append([]byte{0x31}, si.SignedAttrs.FullBytes[1:]...)
	var attrs []attr
	if _, err := asn1.UnmarshalWithParams(signed, &attrs, "set"); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	var digest []byte
	for _, a := range attrs {
		if a.Type.Equal(oidMessageDigest) {
			if _, err := asn1.Unmarshal(a.Values.Bytes, &digest); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := sha256.Sum256(content)
	if !bytes.Equal(digest, want[:]) {
		t.Fatal("messageDigest does not match content")
	}
	h := sha256.Sum256(signed)
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], si.Signature); err != nil {
			t.Fatalf("rsa verify: %v", err)
		}
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(pub, h[:], si.Signature) {
			t.Fatal("ecdsa verify failed")
		}
	}
}

func TestSignDetachedRSAAndECDSA(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for name, key := range map[string]crypto.Signer{"rsa": rk, "ecdsa": ek} {
		t.Run(name, func(t *testing.T) {
			cert := testCert(t, key, "a@example.cz", []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}, time.Now().Add(time.Hour))
			content := []byte("Content-Type: text/plain\r\n\r\nahoj\r\n")
			sig, err := SignDetached(content, key, cert, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			verifyCMS(t, sig, content, cert)
		})
	}
}

func TestSignDetachedRejectsForeignKey(t *testing.T) {
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := testCert(t, k1, "a@example.cz", nil, time.Now().Add(time.Hour))
	if _, err := SignDetached([]byte("x"), k2, cert, nil, time.Now()); err == nil {
		t.Fatal("signing with a key that does not match the certificate must fail")
	}
}

const sample = "From: =?utf-8?q?Martin_Kube=C4=8Dka?= <martin@example.cz>\n" +
	"To: b@example.cz\n" +
	"Subject: Zkouška\n" +
	"Message-ID: <x@example.cz>\n" +
	"MIME-Version: 1.0\n" +
	"Content-Type: multipart/mixed;\n boundary=\"outer\"\n" +
	"\n" +
	"preamble\n" +
	"--outer\n" +
	"Content-Type: multipart/alternative; boundary=\"alt\"\n" +
	"\n" +
	"--alt\n" +
	"Content-Type: text/plain; charset=utf-8\n" +
	"Content-Transfer-Encoding: 8bit\n" +
	"\n" +
	"Příliš žluťoučký kůň   \n" +
	"druhý řádek\n" +
	"--alt\n" +
	"Content-Type: text/html; charset=utf-8\n" +
	"\n" +
	"<p>Ahoj</p>\n" +
	"--alt--\n" +
	"--outer\n" +
	"Content-Type: application/octet-stream; name=\"a.bin\"\n" +
	"Content-Disposition: attachment; filename=\"a.bin\"\n" +
	"\n" +
	"\x01\x02\xff binary\n" +
	"--outer\n" +
	"Content-Type: image/png; name=\"b.png\"\n" +
	"Content-Transfer-Encoding: base64\n" +
	"\n" +
	"iVBORw0KGgo=\n" +
	"--outer--\n" +
	"epilogue\n"

func TestSignMessage(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := testCert(t, key, "martin@example.cz", nil, time.Now().Add(time.Hour))
	var signedContent []byte
	out, err := SignMessage([]byte(sample), func(c []byte) ([]byte, error) {
		signedContent = c
		return SignDetached(c, key, cert, nil, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ReplaceAll(out, []byte("\r\n"), nil), []byte("\n")) || bytes.Contains(bytes.ReplaceAll(out, []byte("\r\n"), nil), []byte("\r")) {
		t.Fatal("output has bare line breaks")
	}
	for _, b := range signedContent {
		if b >= 0x80 {
			t.Fatal("signed part is not 7-bit")
		}
	}

	msg, err := mail.ReadMessage(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Subject") != "Zkouška" || msg.Header.Get("Message-ID") != "<x@example.cz>" {
		t.Fatalf("outer headers lost: %v", msg.Header)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/signed" || params["protocol"] != "application/pkcs7-signature" || params["micalg"] != "sha-256" {
		t.Fatalf("content-type %q %v %v", mt, params, err)
	}
	if strings.Count(string(out), "MIME-Version") != 1 {
		t.Fatal("MIME-Version must appear once")
	}

	// 第一部分的字节必须与签名时的一致 (按 RFC 1847 拆出来)。
	body, _ := io.ReadAll(msg.Body)
	parts, _, _, err := splitMultipart(body, params["boundary"])
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts %d %v", len(parts), err)
	}
	if !bytes.Equal(parts[0], signedContent) {
		t.Fatal("first part differs from the signed content")
	}
	h, b, _ := splitEntity(parts[1])
	if headerValue(h, "Content-Transfer-Encoding") != "base64" {
		t.Fatal("signature part must be base64")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(b), "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	verifyCMS(t, sig, signedContent, cert)

	// 内容仍能被标准库完整读出: 文本保留行尾空格与变音符, 附件字节不变。
	ih, ib, _ := splitEntity(signedContent)
	_, ip, _ := mime.ParseMediaType(headerValue(ih, "Content-Type"))
	r := multipart.NewReader(bytes.NewReader(ib), ip["boundary"])
	var texts []string
	var bin []byte
	for {
		p, err := r.NextRawPart()
		if err != nil {
			break
		}
		ct, pp, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		switch {
		case strings.HasPrefix(ct, "multipart/"):
			ar := multipart.NewReader(p, pp["boundary"])
			for {
				q, err := ar.NextRawPart()
				if err != nil {
					break
				}
				if q.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
					t.Fatalf("text part encoding %q", q.Header.Get("Content-Transfer-Encoding"))
				}
				d, _ := io.ReadAll(quotedprintable.NewReader(q))
				texts = append(texts, string(d))
			}
		case ct == "application/octet-stream":
			d, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
			bin = d
		}
	}
	if len(texts) != 2 || texts[0] != "Příliš žluťoučký kůň   \r\ndruhý řádek" || texts[1] != "<p>Ahoj</p>" {
		t.Fatalf("texts %q", texts)
	}
	if string(bin) != "\x01\x02\xff binary" {
		t.Fatalf("binary %q", bin)
	}
}

func TestCheckUsable(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Now()
	ok := testCert(t, key, "Martin@Example.cz", []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}, now.Add(time.Hour))
	if p := CheckUsable(ok, "martin@example.cz", now); p != "" {
		t.Fatalf("usable cert: %s", p)
	}
	if p := CheckUsable(ok, "jiny@example.cz", now); p != ProblemAddress {
		t.Fatalf("address: %q", p)
	}
	if p := CheckUsable(ok, "", now.Add(2*time.Hour)); p != ProblemExpired {
		t.Fatalf("expired: %q", p)
	}
	server := testCert(t, key, "martin@example.cz", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, now.Add(time.Hour))
	if p := CheckUsable(server, "", now); p != ProblemUsage {
		t.Fatalf("usage: %q", p)
	}
}
