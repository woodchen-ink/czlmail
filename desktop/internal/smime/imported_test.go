package smime

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

var errMemNotFound = errors.New("not found")

// memStore 是测试用的内存密钥库。
func memStore() map[string]string {
	m := map[string]string{}
	SetSecretStore(SecretStore{
		Get: func(k string) (string, error) {
			v, ok := m[k]
			if !ok {
				return "", errMemNotFound
			}
			return v, nil
		},
		Set: func(k, v string) error { m[k] = v; return nil },
		Delete: func(k string) error {
			if _, ok := m[k]; !ok {
				return errMemNotFound
			}
			delete(m, k)
			return nil
		},
		NotFound: errMemNotFound,
	})
	return m
}

func TestImportListOpenRemove(t *testing.T) {
	m := memStore()
	roots, ca, leaf, key := testChain(t, "martin@example.cz")
	p12, err := pkcs12.Modern.Encode(key, leaf, []*x509.Certificate{ca}, "heslo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if _, err := Import(p12, "spatne", now); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := Import([]byte("not a p12"), "", now); !errors.Is(err, ErrBadFile) {
		t.Fatalf("garbage: %v", err)
	}

	got, err := Import(p12, "heslo", now)
	if err != nil || got.Thumbprint != Thumbprint(leaf) || got.Subject != "Signer" {
		t.Fatalf("import: %+v %v", got, err)
	}
	// 再导入一次不重复列出。
	if _, err := Import(p12, "heslo", now); err != nil {
		t.Fatal(err)
	}
	list, err := listImported(now)
	if err != nil || len(list) != 1 || list[0].Thumbprint != got.Thumbprint {
		t.Fatalf("list: %+v %v", list, err)
	}

	s, err := openImported(got.Thumbprint)
	if err != nil {
		t.Fatal(err)
	}
	// 系统信任库里没有测试根: 附上 .p12 里的颁发者证书以外的 (根是自签名的, 不附)。
	if len(s.Chain) != 0 {
		t.Fatalf("chain should skip the self-signed root: %d", len(s.Chain))
	}
	msg, err := SignMessage([]byte(sample), func(c []byte) ([]byte, error) {
		return SignDetached(c, s.Key, s.Cert, s.Chain, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := VerifyMessage(msg, "martin@example.cz", now, roots); v == nil || v.Status != StatusValid {
		t.Fatalf("signature from imported key: %+v", v)
	}

	if err := Remove(got.Thumbprint); err != nil {
		t.Fatal(err)
	}
	if _, err := openImported(got.Thumbprint); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open after remove: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("secrets left after remove: %v", m)
	}
	if err := Remove(got.Thumbprint); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestImportRejects(t *testing.T) {
	memStore()
	_, ca, leaf, key := testChain(t, "martin@example.cz")
	now := time.Now()

	// 私钥与证书不配对。
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	p12, err := pkcs12.Modern.Encode(other, leaf, nil, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(p12, "x", now); !errors.Is(err, ErrBadFile) {
		t.Fatalf("mismatched key: %v", err)
	}

	// 证书上没有邮箱地址 (自签一张只有名字的证书)。
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "No Mail"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	noMail, _ := x509.ParseCertificate(der)
	p12, err = pkcs12.Modern.Encode(key, noMail, []*x509.Certificate{ca}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(p12, "x", now); !errors.Is(err, ErrNotSigning) {
		t.Fatalf("certificate without email: %v", err)
	}
}

func TestImportUnsupportedWithoutStore(t *testing.T) {
	imported.mu.Lock()
	saved := imported.s
	imported.s = nil
	imported.mu.Unlock()
	defer func() { imported.s = saved }()
	if _, err := Import(nil, "", time.Now()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}
