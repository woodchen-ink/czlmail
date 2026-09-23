// Package platform 封装各操作系统的集成: 开机自启、协议与文件关联、默认应用、
// 用系统程序打开文件、窗口前台切换, 以及清理进程的启动与等待。
package platform

// BackgroundFlag 表示开机自启: 启动后直接留在托盘, 不弹窗口。
const BackgroundFlag = "--background"

// LinkScheme 是「复制邮件链接」生成的协议: czlmail://email/<账号>/<邮件id>、czlmail://thread/<账号>/<会话id>。
// 链接只含服务器内部 id, 不含主题、地址等内容, 贴到别处不会泄露邮件信息。
const LinkScheme = "czlmail"
