package update

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrAppDirNotWritable 表示 .app 所在目录当前用户写不进去(如非管理员装在 /Applications),
// 新版本已在访达中显示, 需要用户手动拖入。
var ErrAppDirNotWritable = errors.New("application folder is not writable")

// InstallApp 解压更新包里的新版 .app, 交给一个独立的 shell 进程在本进程退出后
// 替换正在运行的 .app 并重新打开。调用方随后应退出应用。
//
// 不走 DMG: 用户把新版本拖进「应用程序」时旧版本还在运行, 访达会报"正在使用中"。
func InstallApp(archive string) error {
	current, err := currentAppBundle()
	if err != nil {
		return err
	}
	staging := filepath.Join(filepath.Dir(archive), "staging")
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	// ditto 保留 .app 内的符号链接、可执行权限与签名, unzip 会丢。
	if out, err := exec.Command("ditto", "-x", "-k", archive, staging).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip update: %v: %s", err, out)
	}
	matches, _ := filepath.Glob(filepath.Join(staging, "*.app"))
	if len(matches) == 0 {
		return errors.New("no .app in update archive")
	}
	next := matches[0]

	if !dirWritable(filepath.Dir(current)) {
		_ = exec.Command("open", "-R", next).Start()
		return ErrAppDirNotWritable
	}

	// 先改名旧版本再移入新版本, 任一步失败都把旧版本挪回去, 保证总有一个能打开的 .app。
	script := `pid="$1"; cur="$2"; next="$3"
while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
rm -rf "$cur.old"
mv "$cur" "$cur.old" && mv "$next" "$cur" && rm -rf "$cur.old" || { [ -d "$cur.old" ] && [ ! -d "$cur" ] && mv "$cur.old" "$cur"; }
xattr -dr com.apple.quarantine "$cur" 2>/dev/null
open "$cur"`
	cmd := exec.Command("/bin/sh", "-c", script, "czlmail-update", strconv.Itoa(os.Getpid()), current, next)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// currentAppBundle 返回正在运行的 .app 路径(…/czlmail.app/Contents/MacOS/czlmail → …/czlmail.app)。
func currentAppBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("not running from an .app bundle")
	}
	// 从 DMG 或下载目录直接运行时会被 App Translocation 挪到只读的随机路径, 替换不了。
	if strings.Contains(bundle, "/AppTranslocation/") || strings.HasPrefix(bundle, "/Volumes/") {
		return "", errors.New("app is running from a read-only location; move it to Applications first")
	}
	return bundle, nil
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".czlmail-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
