//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

// 把窗口从托盘恢复时拉到前台。
//
// Windows 不让后台进程随便抢前台: 光调 SetForegroundWindow 往往只闪一下任务栏。
// 常见的偷懒写法是把窗口设成 TOPMOST 再取消 —— 能抢到前台, 但只要"取消"那一步
// 没落地(跨线程排队、窗口当时还没显示出来), 窗口就会一直压在所有程序上面,
// 用户点别的程序也盖不住它。这里全程不碰 WS_EX_TOPMOST, 改用 AttachThreadInput:
// 把当前线程挂到前台窗口所属线程的输入队列上, 前台权限检查便会通过。

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")

	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThreadID  = kernel32.NewProc("GetCurrentThreadId")
	procGetCurrentProcessID = kernel32.NewProc("GetCurrentProcessId")
)

const (
	swShow    = 5
	swRestore = 9

	// Wails 注册窗口类时用的固定类名, 见 wails/internal/frontend/desktop/windows/window.go。
	// 我们没有覆盖 windows.Options.WindowClassName, 所以主窗口一定是这个类。
	wailsWindowClass = "wailsWindow"
)

var (
	// 回调只注册一次: syscall.NewCallback 占用的回调槽不会释放。
	enumCallback = syscall.NewCallback(enumProc)
	enumPID      uintptr
	enumFound    uintptr

	mainHWND uintptr
)

func enumProc(hwnd, _ uintptr) uintptr {
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if uintptr(pid) != enumPID {
		return 1
	}
	buf := make([]uint16, 64)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || syscall.UTF16ToString(buf[:n]) != wailsWindowClass {
		return 1
	}
	enumFound = hwnd
	return 0
}

// findMainWindow 找出本进程的主窗口句柄。Wails v2 的运行时不对外暴露 HWND,
// 只能按进程 + 窗口类名找。找到后缓存, 窗口在进程生命周期内不会重建。
func findMainWindow() uintptr {
	if mainHWND != 0 {
		return mainHWND
	}
	pid, _, _ := procGetCurrentProcessID.Call()
	enumPID = pid
	enumFound = 0
	procEnumWindows.Call(enumCallback, 0)
	mainHWND = enumFound
	return mainHWND
}

// raiseToForeground 显示窗口并把它切到前台。不改变窗口的 Z 序属性,
// 因此不会出现"永远盖在别的程序上面"。
func raiseToForeground() {
	hwnd := findMainWindow()
	if hwnd == 0 {
		return
	}

	// AttachThreadInput 作用于调用线程, 整段必须留在同一个系统线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Wails 的 WindowShow 是投递到窗口线程上异步执行的, 这里同步再做一次,
	// 免得窗口还没显示出来就去抢前台(对隐藏窗口 SetForegroundWindow 不生效)。
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}

	fg, _, _ := procGetForegroundWindow.Call()
	if fg == hwnd {
		return
	}

	cur, _, _ := procGetCurrentThreadID.Call()
	var fgThread uintptr
	if fg != 0 {
		fgThread, _, _ = procGetWindowThreadProcessID.Call(fg, 0)
	}
	if fgThread != 0 && fgThread != cur {
		procAttachThreadInput.Call(cur, fgThread, 1)
		defer procAttachThreadInput.Call(cur, fgThread, 0)
	}

	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
}
