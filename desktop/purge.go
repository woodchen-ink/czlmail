package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zalando/go-keyring"
)

// 删除本机的全部数据: 缓存库、配置、日志、界面偏好(WebView2 数据)、已打开的附件与网盘文件、
// 系统钥匙串里的凭据与 AI 密钥、开机自启与协议登记。服务器上的邮件、日历、联系人不受影响。
//
// 程序运行时缓存库和 WebView2 数据目录都被占用删不掉, 因此由一个独立的清理进程
// (`czlmail purge --wait-pid <pid>`)等主程序退出后再删。卸载程序也调用同一个入口。

const purgeArg = "purge"

// DeleteAllData 停止同步、关闭缓存库, 启动清理进程后退出程序。
func (a *App) DeleteAllData() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("2230 locate executable: %w", err)
	}
	cmd := exec.Command(exe, purgeArg, "--wait-pid", strconv.Itoa(os.Getpid()))
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("2231 start cleanup: %w", err)
	}
	_ = cmd.Process.Release()

	a.log.Info("delete all data requested, quitting")
	wruntime.Quit(a.ctx)
	return nil
}

// runPurge 是清理进程的入口, 返回退出码。
func runPurge(args []string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--wait-pid" {
			if pid, err := strconv.Atoi(args[i+1]); err == nil {
				waitProcessExit(pid, 30*time.Second)
			}
		}
	}
	if err := purgeAll(); err != nil {
		fmt.Fprintln(os.Stderr, "purge:", err)
		return 1
	}
	return 0
}

func purgeAll() error {
	var errs []error

	// 开发实例(CZLMAIL_INSTANCE)与正式版共用系统钥匙串和注册表, 只删它自己的目录。
	if os.Getenv("CZLMAIL_INSTANCE") != "" {
		return errors.Join(removeDirs()...)
	}

	// 凭据的条目名取决于配置里的登录方式, 必须先读配置再删目录。
	if cfg, err := LoadConfig(); err == nil {
		if cfg.Username != "" && cfg.SessionEndpoint != "" {
			ignoreNotFound(&errs, keyring.Delete(keyringService, passwordKey(cfg.SessionEndpoint, cfg.Username)))
		}
		if cfg.Issuer != "" {
			ignoreNotFound(&errs, keyring.Delete(keyringService, tokenKey(cfg.Issuer)))
		}
	}
	ignoreNotFound(&errs, keyring.Delete(keyringService, aiKeyringKey))

	if err := setAutostart(false); err != nil && integrationSupported {
		errs = append(errs, err)
	}
	if err := unregisterHandlers(); err != nil {
		errs = append(errs, err)
	}

	errs = append(errs, removeDirs()...)
	return errors.Join(errs...)
}

func ignoreNotFound(errs *[]error, err error) {
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		*errs = append(*errs, err)
	}
}

// removeDirs 删除数据目录、缓存目录以及安装目录下的附件与网盘文件。
func removeDirs() []error {
	var errs []error
	var dirs []string
	if dir, err := dataDir(); err == nil {
		dirs = append(dirs, dir)
	}
	if base, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(base, dataDirName()))
	}
	if exe, err := os.Executable(); err == nil {
		install := filepath.Dir(exe)
		dirs = append(dirs, filepath.Join(install, "attachments"), filepath.Join(install, "files"))
	}
	for _, d := range dirs {
		// WebView2 进程退出稍慢, 目录可能还被占用, 重试几次。
		var err error
		for i := 0; i < 10; i++ {
			if err = removeTree(d); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// removeTree 删除整个目录。Go 1.26.0 的 os.RemoveAll 在部分 Windows 目录上会报
// "unlinkat ...: is a directory" 而删不掉, 失败时改为由深到浅逐个删除。
func removeTree(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	var paths []string
	walkErr := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	// 路径越长层级越深, 先删子项再删父目录。
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	var last error
	for _, p := range paths {
		_ = os.Chmod(p, 0o700) // 去掉只读属性
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			last = err
		}
	}
	return last
}
