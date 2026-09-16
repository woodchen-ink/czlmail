//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows 系统集成: 开机自启、注册为邮件(mailto:)与日历(.ics / webcal:)应用。
//
// 全部写在 HKCU, 不需要管理员权限。Windows 10 起程序不能自己把自己设为默认应用,
// 只能登记"能处理哪些协议与文件", 再打开系统的默认应用设置页由用户确认。

const (
	runKey        = `Software\Microsoft\Windows\CurrentVersion\Run`
	appRegName    = "CZL Mail"
	capabilityKey = `Software\Clients\Mail\CZL Mail\Capabilities`
	progMailto    = "CZLMail.mailto"
	progICS       = "CZLMail.ics"
	progWebcal    = "CZLMail.webcal"
)

func exePath() (string, error) {
	return os.Executable()
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(appRegName)
	if err != nil {
		return false
	}
	exe, _ := exePath()
	// 程序挪了位置(重装到别处)时旧记录指向不存在的路径, 视为未开启。
	return exe != "" && strings.Contains(strings.ToLower(v), strings.ToLower(exe))
}

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(appRegName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := exePath()
	if err != nil {
		return err
	}
	return k.SetStringValue(appRegName, fmt.Sprintf(`"%s" %s`, exe, backgroundFlag))
}

// registerHandlers 登记协议与文件关联的能力。每次启动都执行, 程序路径变化后自动更新。
func registerHandlers() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	command := fmt.Sprintf(`"%s" "%%1"`, exe)
	icon := fmt.Sprintf(`"%s",0`, exe)

	progIDs := []struct{ id, name string }{
		{progMailto, "CZL Mail 邮件链接"},
		{progICS, "CZL Mail 日历文件"},
		{progWebcal, "CZL Mail 日历订阅"},
	}
	for _, p := range progIDs {
		if err := setValues(`Software\Classes\`+p.id, map[string]string{"": p.name}); err != nil {
			return err
		}
		if p.id != progICS {
			if err := setValues(`Software\Classes\`+p.id, map[string]string{"URL Protocol": ""}); err != nil {
				return err
			}
		}
		if err := setValues(`Software\Classes\`+p.id+`\DefaultIcon`, map[string]string{"": icon}); err != nil {
			return err
		}
		if err := setValues(`Software\Classes\`+p.id+`\shell\open\command`, map[string]string{"": command}); err != nil {
			return err
		}
	}

	if err := setValues(capabilityKey, map[string]string{
		"ApplicationName":        appRegName,
		"ApplicationDescription": "JMAP 邮件、日历、通讯录客户端",
		"ApplicationIcon":        icon,
	}); err != nil {
		return err
	}
	if err := setValues(capabilityKey+`\URLAssociations`, map[string]string{"mailto": progMailto, "webcal": progWebcal}); err != nil {
		return err
	}
	if err := setValues(capabilityKey+`\FileAssociations`, map[string]string{".ics": progICS}); err != nil {
		return err
	}
	// 让 .ics 出现在"打开方式"列表里。
	if err := setValues(`Software\Classes\.ics\OpenWithProgids`, map[string]string{progICS: ""}); err != nil {
		return err
	}
	return setValues(`Software\RegisteredApplications`, map[string]string{appRegName: capabilityKey})
}

func setValues(path string, values map[string]string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, v := range values {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

// defaultFor 报告当前用户是否已把某协议/扩展名的默认程序选为本程序。
func defaultFor(kind, name, progID string) bool {
	path := `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\` + name + `\UserChoice`
	if kind == "file" {
		path = `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\` + name + `\UserChoice`
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ProgId")
	return err == nil && strings.EqualFold(v, progID)
}

func isDefaultMail() bool     { return defaultFor("url", "mailto", progMailto) }
func isDefaultCalendar() bool { return defaultFor("file", ".ics", progICS) }

// openDefaultAppsSettings 打开系统默认应用设置, 直接定位到本程序(Windows 11 支持 registeredAppUser 参数)。
func openDefaultAppsSettings() error {
	return openPath("ms-settings:defaultapps?registeredAppUser=" + strings.ReplaceAll(appRegName, " ", "%20"))
}

const integrationSupported = true
