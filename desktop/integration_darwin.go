//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework CoreServices
#import <Cocoa/Cocoa.h>
#import <CoreServices/CoreServices.h>
#include <stdlib.h>
#include <string.h>

// LaunchServices 的这组函数在新系统上标记为弃用, 但仍然有效, 且不需要 UniformTypeIdentifiers
// (macOS 11+)与签名; NSWorkspace 的替代接口要 macOS 12 并会弹系统确认框。

static const char *czlBundleID(void) {
    NSString *bid = [[NSBundle mainBundle] bundleIdentifier];
    return bid == nil ? NULL : strdup([bid UTF8String]);
}

// czlRegisterBundle 让 LaunchServices 重新读取本程序的 Info.plist(协议与 .ics 声明)。
static int czlRegisterBundle(void) {
    NSURL *url = [[NSBundle mainBundle] bundleURL];
    if (url == nil) return -1;
    return (int)LSRegisterURL((CFURLRef)url, true);
}

static int czlIsDefault(int calendar, const char *bundleID) {
    CFStringRef handler = calendar
        ? LSCopyDefaultRoleHandlerForContentType(CFSTR("com.apple.ical.ics"), kLSRolesAll)
        : LSCopyDefaultHandlerForURLScheme(CFSTR("mailto"));
    if (handler == NULL) return 0;
    NSString *want = [NSString stringWithUTF8String:bundleID];
    int same = [(NSString *)handler caseInsensitiveCompare:want] == NSOrderedSame;
    CFRelease(handler);
    return same;
}

static int czlSetDefaults(const char *bundleID) {
    CFStringRef bid = CFStringCreateWithCString(NULL, bundleID, kCFStringEncodingUTF8);
    OSStatus st = LSSetDefaultHandlerForURLScheme(CFSTR("mailto"), bid);
    if (st == noErr) st = LSSetDefaultHandlerForURLScheme(CFSTR("webcal"), bid);
    if (st == noErr) st = LSSetDefaultRoleHandlerForContentType(CFSTR("com.apple.ical.ics"), kLSRolesAll, bid);
    CFRelease(bid);
    return (int)st;
}
*/
import "C"

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// macOS 系统集成: 开机自启(LaunchAgent)、默认邮件与日历应用(LaunchServices)。
//
// 协议与 .ics 的声明在 build/darwin/Info.plist。与 Windows 不同, macOS 允许程序
// 直接把自己设为默认处理程序, 所以「设为默认」一步完成, 不用跳系统设置。

const integrationSupported = true

var errNotBundled = errors.New("2171 CZL Mail is not running from its .app bundle")

func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// launchAgentPath 是开机自启的 LaunchAgent 文件。开发实例用自己的标识, 不覆盖正式版。
func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", instanceID()+".plist"), nil
}

func autostartEnabled() bool {
	path, err := launchAgentPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	exe, _ := exePath()
	// 程序挪了位置后旧文件指向不存在的路径, 视为未开启。
	return exe != "" && bytes.Contains(data, []byte(xmlEscape(exe)))
}

func setAutostart(on bool) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := exePath()
	if err != nil {
		return err
	}
	// 直接从下载目录打开未签名的程序时, 系统把它放到只读的随机路径(App Translocation)运行,
	// 下次登录这个路径就不存在了。
	if strings.Contains(exe, "/AppTranslocation/") {
		return errors.New("2172 请先把 CZL Mail 拖到「应用程序」文件夹再开启开机自启")
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(instanceID()) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>` + backgroundFlag + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plist), 0o644)
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func bundleID() string {
	p := C.czlBundleID()
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

// registerHandlers 让 LaunchServices 登记本程序的协议与文件类型。每次启动执行, 程序挪位置后自动更新。
func registerHandlers() error {
	if bundleID() == "" {
		return nil
	}
	if st := C.czlRegisterBundle(); st != 0 {
		return fmt.Errorf("2173 register with LaunchServices: OSStatus %d", int(st))
	}
	return nil
}

func isDefault(calendar bool) bool {
	id := bundleID()
	if id == "" {
		return false
	}
	cs := C.CString(id)
	defer C.free(unsafe.Pointer(cs))
	flag := C.int(0)
	if calendar {
		flag = 1
	}
	return C.czlIsDefault(flag, cs) != 0
}

func isDefaultMail() bool     { return isDefault(false) }
func isDefaultCalendar() bool { return isDefault(true) }

// openDefaultAppsSettings 在 macOS 上直接把本程序设为 mailto、webcal 与 .ics 的默认处理程序。
func openDefaultAppsSettings() error {
	id := bundleID()
	if id == "" {
		return errNotBundled
	}
	cs := C.CString(id)
	defer C.free(unsafe.Pointer(cs))
	if st := C.czlSetDefaults(cs); st != 0 {
		return fmt.Errorf("2174 set default handlers: OSStatus %d", int(st))
	}
	return nil
}
