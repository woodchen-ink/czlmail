//go:build !windows

package platform

import (
	"os/exec"
	"syscall"
	"time"
)

func DetachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// WaitProcessExit 轮询进程是否还在(信号 0 只检查存在性)。
func WaitProcessExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
