package keychain

import (
	"errors"
	"sync"
)

// Store 是原生壳提供的凭据存储。Android 入口启动时经 SetNative 注册。
type Store interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

var native struct {
	mu sync.RWMutex
	s  Store
}

var errNoStore = errors.New("2020 native secret store not registered")

// SetNative 注册原生凭据存储。
func SetNative(s Store) {
	native.mu.Lock()
	native.s = s
	native.mu.Unlock()
}

func current() (Store, error) {
	native.mu.RLock()
	defer native.mu.RUnlock()
	if native.s == nil {
		return nil, errNoStore
	}
	return native.s, nil
}

func get(service, user string) (string, error) {
	s, err := current()
	if err != nil {
		return "", err
	}
	return s.Get(service, user)
}

func set(service, user, secret string) error {
	s, err := current()
	if err != nil {
		return err
	}
	return s.Set(service, user, secret)
}

func del(service, user string) error {
	s, err := current()
	if err != nil {
		return err
	}
	return s.Delete(service, user)
}
