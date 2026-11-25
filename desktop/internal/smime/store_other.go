//go:build !windows

package smime

import (
	"errors"
	"time"
)

// ErrNotFound 表示证书存储里没有这张证书。
var ErrNotFound = errors.New("smime: certificate not found")

// List 在没有证书存储支持的系统上返回 ErrUnsupported。
func List(time.Time) ([]Certificate, error) { return nil, ErrUnsupported }

// Open 在没有证书存储支持的系统上返回 ErrUnsupported。
func Open(string) (*Signer, error) { return nil, ErrUnsupported }
