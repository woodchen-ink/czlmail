// Package syncseal 用同步口令加密要经网盘跨设备同步的机密 (附加登录的应用专用密码)。
//
// 口令经 Argon2id 推导出 256 位密钥, 每条机密用 AES-256-GCM 单独加密, 附加数据是这条机密的同步键:
// 能改网盘文件的人 (服务器管理员、拿到主账号的人) 不能把一条密文挪到另一个登录名下。
// 网盘里只存盐、推导参数与一段校验密文, 口令与密钥都不离开设备。
package syncseal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// MinPassphrase 是同步口令的最短长度 (字符数)。
const MinPassphrase = 8

const (
	kdfArgon2id = "argon2id"
	// 推导参数取 RFC 9106 的第二推荐档 (64 MiB, 3 轮), 手机上一次推导也在一秒左右。
	defaultTime    = 3
	defaultMemory  = 64 * 1024
	defaultThreads = 4
	// checkText 是校验密文的明文: 用来判断输入的口令对不对, 不必解开任何一条真正的机密。
	checkText = "czlmail sync passphrase check"
	checkAAD  = "check"
)

var (
	// ErrWrongPassphrase 输入的口令解不开校验密文。
	ErrWrongPassphrase = errors.New("2185 the sync passphrase is wrong")
	// ErrShortPassphrase 口令太短。
	ErrShortPassphrase = fmt.Errorf("2186 the sync passphrase must be at least %d characters", MinPassphrase)
	// errBadParams 网盘里的参数不完整或超出范围 (被改坏、或来自不认识的版本)。
	errBadParams = errors.New("2187 the synced encryption settings are damaged")
)

// Params 是推导密钥用的参数, 与校验密文一起存在网盘文件里。
type Params struct {
	KDF     string `json:"kdf"`
	Salt    string `json:"salt"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"`
	Threads uint8  `json:"threads"`
	// Check 是用这组参数推导出的密钥加密 checkText 的结果。
	Check string `json:"check"`
}

// SameKey 报告两组参数是否推导同一个密钥 (盐与参数都相同)。口令在别的设备上改过之后盐会变。
func (p Params) SameKey(o Params) bool {
	return p.KDF == o.KDF && p.Salt == o.Salt && p.Time == o.Time && p.Memory == o.Memory && p.Threads == o.Threads
}

func (p Params) valid() bool {
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	// 上限防止被改坏的文件让设备推导时耗尽内存或卡住。
	return err == nil && len(salt) >= 16 && p.KDF == kdfArgon2id &&
		p.Time >= 1 && p.Time <= 10 && p.Memory >= 8*1024 && p.Memory <= 256*1024 && p.Threads >= 1 && p.Threads <= 16
}

// New 用新的随机盐为 passphrase 生成参数与密钥。
func New(passphrase string) (Params, []byte, error) {
	if len([]rune(passphrase)) < MinPassphrase {
		return Params{}, nil, ErrShortPassphrase
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Params{}, nil, err
	}
	p := Params{
		KDF: kdfArgon2id, Salt: base64.StdEncoding.EncodeToString(salt),
		Time: defaultTime, Memory: defaultMemory, Threads: defaultThreads,
	}
	key := derive(passphrase, p)
	check, err := Seal(key, checkAAD, checkText)
	if err != nil {
		return Params{}, nil, err
	}
	p.Check = check
	return p, key, nil
}

// Unlock 用已有的参数推导密钥并核对口令。
func Unlock(passphrase string, p Params) ([]byte, error) {
	if !p.valid() {
		return nil, errBadParams
	}
	key := derive(passphrase, p)
	if !Verify(key, p) {
		return nil, ErrWrongPassphrase
	}
	return key, nil
}

// Verify 报告 key 是否是这组参数对应的密钥。
func Verify(key []byte, p Params) bool {
	got, err := Open(key, checkAAD, p.Check)
	return err == nil && subtle.ConstantTimeCompare([]byte(got), []byte(checkText)) == 1
}

func derive(passphrase string, p Params) []byte {
	salt, _ := base64.StdEncoding.DecodeString(p.Salt)
	return argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, 32)
}

// Seal 加密 plaintext, 结果是 base64(随机数 || 密文)。aad 是这条机密的归属 (同步键), 解密时必须一致。
func Seal(key []byte, aad, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open 解开 Seal 的结果; 密钥不对、密文被改或 aad 对不上时报错。
func Open(key []byte, aad, sealed string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("2188 cannot decrypt a synced secret")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(aad))
	if err != nil {
		return "", errors.New("2188 cannot decrypt a synced secret")
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
