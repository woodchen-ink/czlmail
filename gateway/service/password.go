package service

import "github.com/woodchen-ink/czlmail/gateway/pkg/crypto"

func hashPassword(p string) (string, error) { return crypto.HashPassword(p) }

func checkPassword(p, hash string) bool {
	ok, err := crypto.VerifyPassword(p, hash)
	return err == nil && ok
}
