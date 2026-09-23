//go:build windows

package platform

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// DetachProcess 让清理进程脱离主程序, 不弹控制台窗口。
func DetachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// WaitProcessExit 等进程退出, 最多 timeout。
func WaitProcessExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // 已经退出
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	// WebView2 子进程晚于主进程退出, 再等一会儿再删它的数据目录。
	time.Sleep(time.Second)
}
