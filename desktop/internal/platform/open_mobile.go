//go:build ios || android

package platform

import "errors"

// iOS 没有"用系统程序打开某个路径"的命令: 由原生壳弹出预览 / 分享面板。
// iOS 入口启动时注册 Opener; reveal 为 true 表示"在文件夹中显示", iOS 上同样给分享面板。
var Opener func(path string, reveal bool) error

func OpenPath(path string) error { return callOpener(path, false) }

func RevealPath(path string) error { return callOpener(path, true) }

func callOpener(path string, reveal bool) error {
	if Opener == nil {
		return errors.New("native opener not registered")
	}
	return Opener(path, reveal)
}

// MarkFromInternet 仅 Windows 实现。
func MarkFromInternet(string) {}
