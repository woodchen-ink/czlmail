//go:build windows

package smime

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 证书存储: 当前用户的「个人」(MY)。私钥留在系统里 (CNG/CSP、智能卡、令牌),
// 签名通过 NCryptSignHash 完成; 受 PIN 保护的密钥由系统弹出输入框。

var (
	modcrypt32 = windows.NewLazySystemDLL("crypt32.dll")
	modncrypt  = windows.NewLazySystemDLL("ncrypt.dll")

	procCertGetCertificateContextProperty = modcrypt32.NewProc("CertGetCertificateContextProperty")
	procNCryptSignHash                    = modncrypt.NewProc("NCryptSignHash")
	procNCryptFreeObject                  = modncrypt.NewProc("NCryptFreeObject")
)

const (
	certKeyProvInfoPropID = 2
	bcryptPadPKCS1        = 0x2
)

type bcryptPKCS1PaddingInfo struct {
	algID *uint16
}

func openMyStore() (windows.Handle, error) {
	name, _ := windows.UTF16PtrFromString("MY")
	return windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER|windows.CERT_STORE_READONLY_FLAG, uintptr(unsafe.Pointer(name)))
}

func parseContext(ctx *windows.CertContext) (*x509.Certificate, error) {
	der := append([]byte(nil), unsafe.Slice(ctx.EncodedCert, ctx.Length)...)
	return x509.ParseCertificate(der)
}

func hasPrivateKey(ctx *windows.CertContext) bool {
	var size uint32
	r, _, _ := procCertGetCertificateContextProperty.Call(uintptr(unsafe.Pointer(ctx)), certKeyProvInfoPropID, 0, uintptr(unsafe.Pointer(&size)))
	return r != 0
}

// List 列出个人证书存储里带私钥、带邮箱地址的证书。
func List(now time.Time) ([]Certificate, error) {
	store, err := openMyStore()
	if err != nil {
		return nil, fmt.Errorf("smime: open certificate store: %w", err)
	}
	defer windows.CertCloseStore(store, 0)

	var out []Certificate
	var ctx *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if err != nil || ctx == nil {
			break
		}
		if !hasPrivateKey(ctx) {
			continue
		}
		c, err := parseContext(ctx)
		if err != nil || len(Emails(c)) == 0 {
			continue
		}
		out = append(out, Describe(c, now))
	}
	return out, nil
}

// Open 按指纹打开证书与私钥。用完必须调用 Signer.Close。
func Open(thumbprint string) (*Signer, error) {
	hash, err := hex.DecodeString(strings.ReplaceAll(thumbprint, " ", ""))
	if err != nil || len(hash) != 20 {
		return nil, errors.New("smime: invalid thumbprint")
	}
	store, err := openMyStore()
	if err != nil {
		return nil, fmt.Errorf("smime: open certificate store: %w", err)
	}
	blob := windows.CryptHashBlob{Size: uint32(len(hash)), Data: &hash[0]}
	ctx, err := windows.CertFindCertificateInStore(store, windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING, 0,
		windows.CERT_FIND_HASH, unsafe.Pointer(&blob), nil)
	runtime.KeepAlive(hash)
	if err != nil || ctx == nil {
		windows.CertCloseStore(store, 0)
		return nil, ErrNotFound
	}
	cert, err := parseContext(ctx)
	if err != nil {
		windows.CertFreeCertificateContext(ctx)
		windows.CertCloseStore(store, 0)
		return nil, err
	}

	var key windows.Handle
	var spec uint32
	var mustFree bool
	err = windows.CryptAcquireCertificatePrivateKey(ctx, windows.CRYPT_ACQUIRE_ONLY_NCRYPT_KEY_FLAG, nil, &key, &spec, &mustFree)
	windows.CertFreeCertificateContext(ctx)
	windows.CertCloseStore(store, 0)
	if err != nil {
		return nil, fmt.Errorf("smime: private key: %w", err)
	}
	if spec != windows.CERT_NCRYPT_KEY_SPEC {
		return nil, errors.New("smime: private key is not a CNG key")
	}
	closeKey := func() {
		if mustFree {
			procNCryptFreeObject.Call(uintptr(key))
		}
	}
	signer := &ncryptSigner{key: key, pub: cert.PublicKey}
	switch cert.PublicKey.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey:
	default:
		closeKey()
		return nil, fmt.Errorf("smime: unsupported key type %T", cert.PublicKey)
	}
	return &Signer{Cert: cert, Chain: chainFor(cert, nil), Key: signer, Close: closeKey}, nil
}

// ErrNotFound 表示证书存储里没有这张证书 (已删除或换了电脑)。
var ErrNotFound = errors.New("smime: certificate not found")

type ncryptSigner struct {
	key windows.Handle
	pub crypto.PublicKey
}

func (s *ncryptSigner) Public() crypto.PublicKey { return s.pub }

func (s *ncryptSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != 32 {
		return nil, errors.New("smime: only SHA-256 is supported")
	}
	var padding unsafe.Pointer
	var flags uint32
	var info bcryptPKCS1PaddingInfo
	if _, ok := s.pub.(*rsa.PublicKey); ok {
		info.algID, _ = windows.UTF16PtrFromString("SHA256")
		padding = unsafe.Pointer(&info)
		flags = bcryptPadPKCS1
	}
	var size uint32
	r, _, _ := procNCryptSignHash.Call(uintptr(s.key), uintptr(padding), uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)),
		0, 0, uintptr(unsafe.Pointer(&size)), uintptr(flags))
	if r != 0 {
		return nil, fmt.Errorf("smime: NCryptSignHash: 0x%x", uint32(r))
	}
	sig := make([]byte, size)
	r, _, _ = procNCryptSignHash.Call(uintptr(s.key), uintptr(padding), uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)),
		uintptr(unsafe.Pointer(&sig[0])), uintptr(size), uintptr(unsafe.Pointer(&size)), uintptr(flags))
	runtime.KeepAlive(info.algID)
	if r != 0 {
		return nil, fmt.Errorf("smime: NCryptSignHash: 0x%x", uint32(r))
	}
	sig = sig[:size]
	if _, ok := s.pub.(*ecdsa.PublicKey); ok {
		// CNG 给出 r||s, CMS 要 DER 的 SEQUENCE { r, s }。
		half := len(sig) / 2
		return asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:half]), new(big.Int).SetBytes(sig[half:])})
	}
	return sig, nil
}
