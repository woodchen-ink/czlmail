//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// openPath 用关联程序打开文件。走 ShellExecute 而不是 cmd /c start:
// 后者会把文件名里的 & ^ 之类当作命令语法解析, 而附件名由发件人决定。
func openPath(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

// revealPath 在资源管理器中选中文件。
func revealPath(path string) error {
	cmd := exec.Command("explorer.exe")
	// "/select," 与路径必须原样拼成一行, 经 Go 的默认参数转义后 explorer 会读错。
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	// explorer 成功时也常以非零码退出, 只看能否启动。
	return cmd.Start()
}

// markFromInternet 写入 Mark-of-the-Web。失败(如非 NTFS 卷)不影响使用。
func markFromInternet(path string) {
	_ = os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o600)
}
