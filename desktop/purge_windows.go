//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// detachProcess 让清理进程脱离主程序, 不弹控制台窗口。
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// waitProcessExit 等进程退出, 最多 timeout。
func waitProcessExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // 已经退出
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	// WebView2 子进程晚于主进程退出, 再等一会儿再删它的数据目录。
	time.Sleep(time.Second)
}

// unregisterHandlers 删除 registerHandlers 写入的协议、文件关联与应用登记。
func unregisterHandlers() error {
	keys := []string{
		`Software\Classes\` + progMailto,
		`Software\Classes\` + progICS,
		`Software\Classes\` + progWebcal,
		`Software\Clients\Mail\CZL Mail`,
	}
	for _, k := range keys {
		_ = deleteKeyTree(registry.CURRENT_USER, k)
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(appRegName)
		k.Close()
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\.ics\OpenWithProgids`, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(progICS)
		k.Close()
	}
	return nil
}

// deleteKeyTree 递归删除注册表项(registry.DeleteKey 只能删没有子项的项)。
func deleteKeyTree(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	names, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, n := range names {
		if err := deleteKeyTree(root, path+`\`+n); err != nil {
			return err
		}
	}
	return registry.DeleteKey(root, path)
}
