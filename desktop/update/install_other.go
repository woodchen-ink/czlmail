//go:build !darwin

package update

import "errors"

// ErrAppDirNotWritable 仅 macOS 使用。
var ErrAppDirNotWritable = errors.New("application folder is not writable")

// InstallApp 仅 macOS 实现; Windows 直接运行 NSIS 安装程序。
func InstallApp(string) error {
	return errors.New("InstallApp is only supported on macOS")
}
