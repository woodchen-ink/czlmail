package main

// 宿主能力: 文件对话框、向界面推事件、用系统浏览器打开链接、退出程序。
//
// 桌面端由 Wails 运行时提供 (host_desktop.go), iOS 由原生壳提供 (host_ios.go)。
// 绑定方法只调这里的函数, 不直接 import Wails —— iOS 构建里没有 Wails。

// fileFilter 是对话框里的一类文件, Pattern 形如 "*.eml;*.zip"。
type fileFilter struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// fileDialog 描述一次文件选择或保存。
type fileDialog struct {
	Title           string       `json:"title"`
	DefaultFilename string       `json:"defaultFilename,omitempty"`
	Filters         []fileFilter `json:"filters,omitempty"`
}

// 以下宿主方法的约定 (各平台一致):
//   - 用户取消时返回空值且 err 为 nil
//   - 对话框打不开才返回 err
//
// a.saveFileDialog(d) (string, error)
// a.openFileDialog(d) (string, error)
// a.openFilesDialog(d) ([]string, error)
// a.openDirectoryDialog(d) (string, error)
// a.openURL(url)
// a.quit()
// a.emit(name, data...)
