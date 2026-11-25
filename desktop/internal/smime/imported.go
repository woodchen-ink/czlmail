package smime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// 导入的证书: 手机上没有本程序能直接调用的系统证书存储, 用户导入 .p12 / .pfx,
// 私钥与证书链存进系统密钥库 (iOS 钥匙串 / Android Keystore 加密), 签名在 Go 里做。
// 密钥库里每张证书一个条目, 另有一个条目记全部指纹, 供列表用。

// SecretStore 是存放导入证书的密钥库, 由宿主注册 (见 SetSecretStore)。
type SecretStore struct {
	Get    func(key string) (string, error)
	Set    func(key, value string) error
	Delete func(key string) error
	// NotFound 是 Get / Delete 在条目不存在时返回的错误。
	NotFound error
}

var (
	// ErrBadPassword 表示 .p12 的密码不对。
	ErrBadPassword = errors.New("smime: wrong certificate password")
	// ErrBadFile 表示文件不是能用的 PKCS#12 (格式不对, 或里面没有私钥与证书)。
	ErrBadFile = errors.New("smime: not a usable PKCS#12 file")
	// ErrNotSigning 表示证书不能用于签名邮件 (不含邮箱地址, 或密钥类型不支持)。
	ErrNotSigning = errors.New("smime: certificate cannot sign email")
)

const importedIndexKey = "smime-imported"

func importedKey(thumbprint string) string { return importedIndexKey + ":" + thumbprint }

type importedEntry struct {
	// Key 是 PKCS#8 私钥 (base64)。
	Key string `json:"key"`
	// Certs 是 DER 证书 (base64), 第一张是签名证书, 其后是 .p12 里附带的颁发者证书。
	Certs []string `json:"certs"`
}

var imported struct {
	mu sync.Mutex
	s  *SecretStore
}

// SetSecretStore 注册导入证书用的密钥库。没有注册的平台 (桌面端用系统证书存储) 不支持导入。
func SetSecretStore(s SecretStore) {
	imported.mu.Lock()
	imported.s = &s
	imported.mu.Unlock()
}

func importStore() (*SecretStore, error) {
	if imported.s == nil {
		return nil, ErrUnsupported
	}
	return imported.s, nil
}

// Import 导入 .p12 / .pfx: 校验后把私钥与证书链存进密钥库。同一张证书再导入一次覆盖原条目。
func Import(data []byte, password string, now time.Time) (Certificate, error) {
	imported.mu.Lock()
	defer imported.mu.Unlock()
	s, err := importStore()
	if err != nil {
		return Certificate{}, err
	}
	key, leaf, cas, err := pkcs12.DecodeChain(data, password)
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return Certificate{}, ErrBadPassword
	}
	if err != nil || key == nil || leaf == nil {
		return Certificate{}, ErrBadFile
	}
	signer, ok := key.(crypto.Signer)
	if !ok || !samePublicKey(signer.Public(), leaf.PublicKey) {
		return Certificate{}, ErrBadFile
	}
	switch leaf.PublicKey.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey:
	default:
		return Certificate{}, ErrNotSigning
	}
	if len(Emails(leaf)) == 0 {
		return Certificate{}, ErrNotSigning
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Certificate{}, ErrNotSigning
	}
	entry := importedEntry{Key: base64.StdEncoding.EncodeToString(der)}
	for _, c := range append([]*x509.Certificate{leaf}, cas...) {
		entry.Certs = append(entry.Certs, base64.StdEncoding.EncodeToString(c.Raw))
	}
	raw, _ := json.Marshal(entry)
	thumb := Thumbprint(leaf)
	if err := s.Set(importedKey(thumb), string(raw)); err != nil {
		return Certificate{}, fmt.Errorf("smime: store certificate: %w", err)
	}
	index, err := readIndex(s)
	if err != nil {
		return Certificate{}, err
	}
	if !slices.Contains(index, thumb) {
		if err := writeIndex(s, append(index, thumb)); err != nil {
			return Certificate{}, err
		}
	}
	return Describe(leaf, now), nil
}

// Remove 从密钥库删除导入的证书; 不存在时不报错。
func Remove(thumbprint string) error {
	imported.mu.Lock()
	defer imported.mu.Unlock()
	s, err := importStore()
	if err != nil {
		return err
	}
	if err := s.Delete(importedKey(thumbprint)); err != nil && !errors.Is(err, s.NotFound) {
		return fmt.Errorf("smime: remove certificate: %w", err)
	}
	index, err := readIndex(s)
	if err != nil {
		return err
	}
	return writeIndex(s, slices.DeleteFunc(index, func(t string) bool { return t == thumbprint }))
}

// listImported 列出导入的证书; 读不出的条目 (被系统清掉等) 跳过。
func listImported(now time.Time) ([]Certificate, error) {
	imported.mu.Lock()
	defer imported.mu.Unlock()
	s, err := importStore()
	if err != nil {
		return nil, err
	}
	index, err := readIndex(s)
	if err != nil {
		return nil, err
	}
	out := []Certificate{}
	for _, thumb := range index {
		if e, err := readEntry(s, thumb); err == nil {
			out = append(out, Describe(e.leaf, now))
		}
	}
	return out, nil
}

// openImported 取出导入证书的私钥, 返回签名用的 Signer。
func openImported(thumbprint string) (*Signer, error) {
	imported.mu.Lock()
	defer imported.mu.Unlock()
	s, err := importStore()
	if err != nil {
		return nil, err
	}
	e, err := readEntry(s, thumbprint)
	if errors.Is(err, s.NotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	chain := chainFor(e.leaf, e.issuers)
	if chain == nil {
		// 系统信任库补不全 (私有 CA 等): 附上 .p12 里带的颁发者证书, 收件人信任那个根时就能核对。
		for _, c := range e.issuers {
			if !bytes.Equal(c.RawIssuer, c.RawSubject) {
				chain = append(chain, c)
			}
		}
	}
	return &Signer{Cert: e.leaf, Chain: chain, Key: e.key}, nil
}

type decodedEntry struct {
	leaf    *x509.Certificate
	issuers []*x509.Certificate
	key     crypto.Signer
}

func readEntry(s *SecretStore, thumbprint string) (*decodedEntry, error) {
	raw, err := s.Get(importedKey(thumbprint))
	if err != nil {
		return nil, err
	}
	var e importedEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil || len(e.Certs) == 0 {
		return nil, ErrBadFile
	}
	keyDER, err := base64.StdEncoding.DecodeString(e.Key)
	if err != nil {
		return nil, ErrBadFile
	}
	key, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, ErrBadFile
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrBadFile
	}
	var certs []*x509.Certificate
	for _, b := range e.Certs {
		der, err := base64.StdEncoding.DecodeString(b)
		if err != nil {
			return nil, ErrBadFile
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrBadFile
		}
		certs = append(certs, c)
	}
	return &decodedEntry{leaf: certs[0], issuers: certs[1:], key: signer}, nil
}

func readIndex(s *SecretStore) ([]string, error) {
	raw, err := s.Get(importedIndexKey)
	if errors.Is(err, s.NotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("smime: read certificate list: %w", err)
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, nil
	}
	return list, nil
}

func writeIndex(s *SecretStore, list []string) error {
	if len(list) == 0 {
		if err := s.Delete(importedIndexKey); err != nil && !errors.Is(err, s.NotFound) {
			return fmt.Errorf("smime: write certificate list: %w", err)
		}
		return nil
	}
	raw, _ := json.Marshal(list)
	if err := s.Set(importedIndexKey, string(raw)); err != nil {
		return fmt.Errorf("smime: write certificate list: %w", err)
	}
	return nil
}

func samePublicKey(a, b crypto.PublicKey) bool {
	x, err1 := x509.MarshalPKIXPublicKey(a)
	y, err2 := x509.MarshalPKIXPublicKey(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}
