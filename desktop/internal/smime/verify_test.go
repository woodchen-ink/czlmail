package smime

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"testing"
	"time"
)

// testChain 返回 根证书池、签名证书、私钥: 根 → 签名证书 (邮件保护, 地址 from)。
func testChain(t *testing.T, from string) (*x509.CertPool, *x509.Certificate, *x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(77), Subject: pkix.Name{CommonName: "Signer"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(2 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		EmailAddresses: []string{from},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	leaf, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return roots, ca, leaf, key
}

func signedSample(t *testing.T, leaf *x509.Certificate, key *rsa.PrivateKey) []byte {
	t.Helper()
	out, err := SignMessage([]byte(sample), func(c []byte) ([]byte, error) {
		return SignDetached(c, key, leaf, nil, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifyMessage(t *testing.T) {
	roots, _, leaf, key := testChain(t, "martin@example.cz")
	msg := signedSample(t, leaf, key)
	now := time.Now()

	v, err := VerifyMessage(msg, "Martin@Example.cz", now, roots)
	if err != nil || v == nil || v.Status != StatusValid {
		t.Fatalf("valid: %+v %v", v, err)
	}
	if v.Signer == nil || v.Signer.Subject != "Signer" || v.SignedAt.IsZero() {
		t.Fatalf("signer details: %+v", v)
	}

	for _, tc := range []struct {
		name, from, want string
		at               time.Time
	}{
		{"leaf expired", "martin@example.cz", StatusUntrusted, leaf.NotAfter.Add(time.Minute)},
		{"not yet valid", "martin@example.cz", StatusUntrusted, leaf.NotBefore.Add(-time.Minute)},
		{"unknown sender", "", "sender_unknown", now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := VerifyMessage(msg, tc.from, tc.at, roots)
			if err != nil || v == nil || v.Status != tc.want {
				t.Fatalf("want %s, got %+v (%v)", tc.want, v, err)
			}
		})
	}
	// 改动被签名部分的一个字母。
	tampered := bytes.Replace(msg, []byte("<p>Ahoj</p>"), []byte("<p>Ahoi</p>"), 1)
	if bytes.Equal(tampered, msg) {
		t.Fatal("sample text not found")
	}
	if v, _ := VerifyMessage(tampered, "martin@example.cz", now, roots); v == nil || v.Status != StatusInvalid {
		t.Fatalf("tampered: %+v", v)
	}

	// 外层头 (主题) 不在签名范围内, 改了签名照样有效。
	retitled := bytes.Replace(msg, []byte("Subject: Zkouška"), []byte("Subject: Jiný"), 1)
	if v, _ := VerifyMessage(retitled, "martin@example.cz", now, roots); v == nil || v.Status != StatusValid {
		t.Fatalf("outer header change: %+v", v)
	}

	if v, _ := VerifyMessage(msg, "martin@example.cz", now, x509.NewCertPool()); v == nil || v.Status != StatusUntrusted {
		t.Fatalf("untrusted: %+v", v)
	}
	if v, _ := VerifyMessage(msg, "jiny@example.cz", now, roots); v == nil || v.Status != StatusAddress {
		t.Fatalf("address: %+v", v)
	}
	if v, err := VerifyMessage([]byte(sample), "martin@example.cz", now, roots); v != nil || err != nil {
		t.Fatalf("unsigned message: %+v %v", v, err)
	}
}

// 真实doručené zprávy (CZLMAIL_SMIME_EML=cesta) se ověřují proti systémovým kořenům.
func TestVerifyRealMessage(t *testing.T) {
	p := os.Getenv("CZLMAIL_SMIME_EML")
	if p == "" {
		t.Skip()
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := VerifyMessage(raw, os.Getenv("CZLMAIL_SMIME_FROM"), time.Now(), nil)
	if err != nil || v == nil {
		t.Fatalf("%+v %v", v, err)
	}
	t.Logf("status=%s signer=%+v signedAt=%s detail=%s", v.Status, v.Signer, v.SignedAt, v.Detail)
	if v.Status != StatusValid {
		t.Fail()
	}
}
