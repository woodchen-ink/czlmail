package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for new hashes (OWASP minimum: 19 MiB, 2 passes, 1 lane).
// Stored hashes carry their own parameters, so raising these later keeps old hashes valid.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots caps concurrent argon2id runs. Each run allocates argonMemory, so a burst of
// failed logins cannot make the gateway allocate memory without bound.
var hashSlots = make(chan struct{}, max(2, runtime.NumCPU()))

// ErrBadHash means a stored hash is not in the format HashPassword writes.
var ErrBadHash = errors.New("crypto: malformed password hash")

// HashPassword returns an argon2id hash in PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches a hash written by HashPassword.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		memory == 0 || memory > 1<<20 || time == 0 || time > 16 || threads == 0 {
		return false, ErrBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 128 {
		return false, ErrBadHash
	}
	got := derive(password, salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when the account does not exist, so a miss costs
// as much time as a wrong password and does not reveal which emails are registered.
var dummyHash, _ = HashPassword("czlmail-gateway dummy password")

// BurnPasswordCheck spends the time of one password verification.
func BurnPasswordCheck(password string) { _, _ = VerifyPassword(password, dummyHash) }

func derive(password string, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
}
