package jmapserver_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// deviceKeys is a push key pair as the czlmail iOS app holds it.
type deviceKeys struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newDeviceKeys(t *testing.T) deviceKeys {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return deviceKeys{priv: priv, auth: auth}
}

func (k deviceKeys) p256dh() string  { return b64(k.priv.PublicKey().Bytes()) }
func (k deviceKeys) authB64() string { return b64(k.auth) }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// decryptLikeIOS is czlmail-mobile's WebPush.decrypt (CzlKit/Sources/CzlPush/WebPush.swift)
// step by step: header salt(16) | rs(4) | idlen(1) | keyid | ciphertext, IKM from
// HKDF(auth, ECDH, "WebPush: info" 0x00 || device key || server key), one record
// whose plaintext ends with 0x02 and optional 0x00 padding.
func decryptLikeIOS(message []byte, keys deviceKeys) ([]byte, error) {
	malformed := errors.New("malformed")
	if len(message) <= 21 {
		return nil, malformed
	}
	salt := message[0:16]
	idlen := int(message[20])
	if len(message) <= 21+idlen+16 {
		return nil, malformed
	}
	serverKeyRaw := message[21 : 21+idlen]
	ciphertext := message[21+idlen:]

	serverKey, err := ecdh.P256().NewPublicKey(serverKeyRaw)
	if err != nil {
		return nil, err
	}
	shared, err := keys.priv.ECDH(serverKey)
	if err != nil {
		return nil, err
	}
	keyInfo := append([]byte("WebPush: info\x00"), keys.priv.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, serverKeyRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, keys.auth, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, malformed
	}
	return plain[:len(plain)-1], nil
}

// RFC 8291 Appendix A.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcMessage   = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func TestPushEncryptionRFC8291Vector(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(unb64(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	ua, err := ecdh.P256().NewPrivateKey(unb64(t, rfcUAPrivate))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64(ua.PublicKey().Bytes()); got != rfcUAPublic {
		t.Fatalf("ua public %s", got)
	}
	// The RFC example uses a record size of 4096, the same as the gateway.
	got, err := jmapserver.EncryptPushWith(as, unb64(t, rfcSalt), unb64(t, rfcUAPublic), unb64(t, rfcAuth), []byte(rfcPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	if b64(got) != rfcMessage {
		t.Fatalf("message\n got %s\nwant %s", b64(got), rfcMessage)
	}
	plain, err := decryptLikeIOS(unb64(t, rfcMessage), deviceKeys{priv: ua, auth: unb64(t, rfcAuth)})
	if err != nil || string(plain) != rfcPlaintext {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
}

func TestPushEncryptionRoundTrip(t *testing.T) {
	keys := newDeviceKeys(t)
	as, _ := ecdh.P256().GenerateKey(rand.Reader)
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	msg := bytes.Repeat([]byte("x"), 2600)
	enc, err := jmapserver.EncryptPushWith(as, salt, keys.priv.PublicKey().Bytes(), keys.auth, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) != len(msg)+103 {
		t.Fatalf("overhead %d, want 103", len(enc)-len(msg))
	}
	plain, err := decryptLikeIOS(enc, keys)
	if err != nil || !bytes.Equal(plain, msg) {
		t.Fatalf("round trip: %v", err)
	}
	// Another sign-in's auth must not open it (czlmail tells sign-ins apart this way).
	other := keys
	other.auth = make([]byte, 16)
	if _, err := decryptLikeIOS(enc, other); err == nil {
		t.Fatal("opened with the wrong auth")
	}
	if _, err := jmapserver.EncryptPushWith(as, salt, keys.priv.PublicKey().Bytes(), keys.auth, make([]byte, 4096)); err == nil {
		t.Fatal("a message over one record was accepted")
	}
}
