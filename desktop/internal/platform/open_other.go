//go:build !windows

package platform

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

func OpenPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}

func RevealPath(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}

// MarkFromInternet 仅 Windows 实现。
func MarkFromInternet(string) {}
