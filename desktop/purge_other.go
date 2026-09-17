//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// waitProcessExit 轮询进程是否还在(信号 0 只检查存在性)。
func waitProcessExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func unregisterHandlers() error { return nil }
