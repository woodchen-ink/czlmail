package main

import (
	"embed"
	"log"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// all: 前缀是必须的 —— Next.js 的导出产物里有以下划线开头的目录 (_next),
// 默认的 embed 规则会把它们整个跳过, 导致运行时只剩一个没有样式与脚本的 HTML。
//
//go:embed all:frontend/out
var assets embed.FS

func main() {
	// `czlmail mcp` 作为 MCP stdio 桥运行, 不启动窗口。见 mcp_bridge.go。
	// `czlmail purge` 删除本机全部数据, 供「删除所有数据」与卸载程序调用。见 purge.go。
	if len(os.Args) > 1 && os.Args[1] == purgeArg {
		os.Exit(runPurge(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(runMCPBridge())
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "CZL Mail",
		Width:     1400,
		Height:    900,
		MinWidth:  960,
		MinHeight: 600,

		AssetServer: &assetserver.Options{
			Assets: assets,
			// 嵌入资源里找不到的请求落到这里: 头像代理与附件预览。
			Handler: app.assetHandler(),
		},

		// 从资源管理器拖入文件: 由前端按 --wails-drop-target 判断落点(网盘、写信附件)。
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},

		// 关闭窗口时缩到托盘继续收信; 真正退出走托盘菜单。
		HideWindowOnClose: hideOnClose,
		// 开机自启时直接留在托盘。
		StartHidden: hideOnClose && slices.Contains(os.Args[1:], backgroundFlag),
		// 程序已在托盘里运行时再次启动, 唤出已有窗口而不是开第二个实例 ——
		// 两个实例会争用同一个缓存库与推送连接。
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: instanceID(),
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				// 从 mailto 链接或 .ics 文件再次启动时, 交给已运行的实例处理。
				if !app.handleArgs(data.Args) {
					app.showWindow()
				}
			},
		},

		OnStartup:  app.startup,
		OnShutdown: app.shutdown,

		Bind: []any{app},

		Windows: &windows.Options{
			// 邮件正文是任意来源的 HTML, 窗口本身不做背景半透明,
			// 免得深色正文透出桌面影响可读性。
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
	})
	if err != nil {
		log.Fatalf("1 run application: %v", err)
	}
}

// instanceID 是单实例锁的标识。开发调试时设置 CZLMAIL_INSTANCE 可以与已安装的正式版同时运行。
func instanceID() string {
	if v := os.Getenv("CZLMAIL_INSTANCE"); v != "" {
		return "net.czl.mail." + v
	}
	return "net.czl.mail"
}
