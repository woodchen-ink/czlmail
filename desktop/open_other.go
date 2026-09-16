//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

func openPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}

func revealPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}

// markFromInternet 仅 Windows 实现。
func markFromInternet(string) {}
