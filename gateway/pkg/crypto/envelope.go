// Package crypto encrypts the secrets the gateway keeps in its database:
// upstream passwords, OAuth tokens, OAuth client secrets and proxy passwords.
//
// Each purpose gets its own key derived from the master key with HKDF-SHA256, and every
// ciphertext is bound to the row and column it belongs to through the AEAD additional data,
// so a ciphertext copied into another row or column fails to open. Ciphertexts carry the
// master key version, which lets `gateway rotate-key` re-encrypt them under a new key.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Purposes. A key derived for one purpose never opens another purpose's data.
const (
	PurposeUpstreamSecret = "upstream-secret" // IMAP / SMTP passwords, OAuth tokens
	PurposeOAuthClient    = "oauth-client"    // Google / Microsoft client secrets
	PurposeProxySecret    = "proxy-secret"    // SOCKS5 passwords
	PurposeKeyCheck       = "key-check"       // proves at startup that the master key is the right one
)

const keySize = 32

// ErrOpen is returned for any ciphertext that cannot be opened: wrong key, wrong
// row or column, wrong purpose, or tampered data. Callers must not tell these apart.
var ErrOpen = errors.New("crypto: cannot open sealed value")

// AAD names where a ciphertext is stored. RowID is the row's public identifier,
// which never changes; the auto-increment key is not used.
type AAD struct {
	Table  string
	RowID  string
	Column string
}

func (a AAD) bytes() []byte {
	return []byte(a.Table + "\x00" + a.RowID + "\x00" + a.Column)
}

// Keyring holds the current master key and, during a rotation, older ones.
type Keyring struct {
	current uint32
	roots   map[uint32][]byte
}

// NewKeyring returns a keyring whose current master key has the given version.
func NewKeyring(version uint32, master []byte) (*Keyring, error) {
	if version == 0 {
		return nil, errors.New("crypto: key version starts at 1")
	}
	if len(master) != keySize {
		return nil, fmt.Errorf("crypto: master key must be %d bytes", keySize)
	}
	return &Keyring{current: version, roots: map[uint32][]byte{version: clone(master)}}, nil
}

// AddOld registers a previous master key so values sealed under it can still be opened.
func (k *Keyring) AddOld(version uint32, master []byte) error {
	if version == 0 || version == k.current {
		return fmt.Errorf("crypto: invalid old key version %d", version)
	}
	if len(master) != keySize {
		return fmt.Errorf("crypto: master key must be %d bytes", keySize)
	}
	k.roots[version] = clone(master)
	return nil
}

// Current is the version new values are sealed with.
func (k *Keyring) Current() uint32 { return k.current }

// Seal encrypts plaintext for purpose and binds it to aad.
// The result looks like "v1.<base64url>" and is safe to store in a text column.
func (k *Keyring) Seal(purpose string, aad AAD, plaintext []byte) (string, error) {
	aead, err := k.aead(k.current, purpose)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := aead.Seal(nonce, nonce, plaintext, aad.bytes())
	return "v" + strconv.FormatUint(uint64(k.current), 10) + "." + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal for the same purpose and aad.
func (k *Keyring) Open(purpose string, aad AAD, sealed string) ([]byte, error) {
	version, body, err := split(sealed)
	if err != nil {
		return nil, ErrOpen
	}
	aead, err := k.aead(version, purpose)
	if err != nil {
		return nil, ErrOpen
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrOpen
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], aad.bytes())
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}

// NeedsRotation reports whether sealed was produced with an older master key.
func (k *Keyring) NeedsRotation(sealed string) bool {
	version, _, err := split(sealed)
	return err == nil && version != k.current
}

func (k *Keyring) aead(version uint32, purpose string) (cipher.AEAD, error) {
	root, ok := k.roots[version]
	if !ok {
		return nil, fmt.Errorf("crypto: unknown key version %d", version)
	}
	if purpose == "" {
		return nil, errors.New("crypto: purpose is required")
	}
	key, err := hkdf.Key(sha256.New, root, nil, "czlmail-gateway/"+purpose, keySize)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func split(sealed string) (uint32, string, error) {
	head, body, ok := strings.Cut(sealed, ".")
	if !ok || len(head) < 2 || head[0] != 'v' || body == "" {
		return 0, "", errors.New("crypto: malformed sealed value")
	}
	v, err := strconv.ParseUint(head[1:], 10, 32)
	if err != nil || v == 0 {
		return 0, "", errors.New("crypto: malformed key version")
	}
	return uint32(v), body, nil
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }
