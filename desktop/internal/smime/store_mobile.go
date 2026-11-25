//go:build ios || android

package smime

import (
	"errors"
	"time"
)

// 手机上只用导入的证书 (imported.go): iOS 的第三方程序取不到描述文件装进系统的身份,
// Android 的系统证书要经 KeyChain 逐个授权, 都不能像桌面那样直接列出来。

// ErrNotFound 表示证书存储里没有这张证书。
var ErrNotFound = errors.New("smime: certificate not found")

// List 列出导入的证书。
func List(now time.Time) ([]Certificate, error) { return listImported(now) }

// Open 打开导入的证书用于签名。
func Open(thumbprint string) (*Signer, error) { return openImported(thumbprint) }
