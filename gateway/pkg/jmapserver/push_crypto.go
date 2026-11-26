package jmapserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Web Push message encryption (RFC 8291) with the aes128gcm content coding (RFC 8188).
// Every message is one record: salt(16) | rs(4) | idlen(1) | server public key(65) | ciphertext.

const (
	pushRecordSize = 4096
	pushHeaderSize = 16 + 4 + 1 + 65
	// pushOverhead is what encryption adds to the plaintext: header, delimiter and GCM tag.
	pushOverhead = pushHeaderSize + 1 + 16
)

var errPushTooLarge = errors.New("push message does not fit in one record")

// encryptPush encrypts plaintext for the user agent's public key and auth secret.
func encryptPush(uaPublic, authSecret, plaintext []byte) ([]byte, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encryptPushWith(priv, salt, uaPublic, authSecret, plaintext)
}

// encryptPushWith is encryptPush with a given ephemeral key and salt (RFC 8291 Appendix A).
func encryptPushWith(priv *ecdh.PrivateKey, salt, uaPublic, authSecret, plaintext []byte) ([]byte, error) {
	if len(plaintext)+1+16 > pushRecordSize {
		return nil, errPushTooLarge
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	secret, err := priv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := priv.PublicKey().Bytes()

	// IKM = HKDF(auth, ECDH secret, "WebPush: info" 0x00 || ua_public || as_public, 32)
	keyInfo := make([]byte, 0, 14+65+65)
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm, err := hkdf.Key(sha256.New, secret, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, pushHeaderSize+len(plaintext)+1+16)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, pushRecordSize)
	out = append(out, byte(len(asPublic)))
	out = append(out, asPublic...)
	record := append(append(make([]byte, 0, len(plaintext)+1), plaintext...), 0x02) // last record: delimiter 2, no padding
	return gcm.Seal(out, nonce, record, nil), nil
}
