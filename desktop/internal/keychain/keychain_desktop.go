//go:build !ios

package keychain

import (
	"errors"

	"github.com/zalando/go-keyring"
)

func get(service, user string) (string, error) {
	v, err := keyring.Get(service, user)
	return v, mapErr(err)
}

func set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func del(service, user string) error {
	return mapErr(keyring.Delete(service, user))
}

func mapErr(err error) error {
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
