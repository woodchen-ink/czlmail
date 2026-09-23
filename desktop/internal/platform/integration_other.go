//go:build !windows && !darwin

package platform

import "errors"

// 非 Windows 平台暂不支持自启与默认应用登记; macOS 需要 LaunchAgent 与 LSSetDefaultHandler(cgo)。

var errIntegrationUnsupported = errors.New("2170 not supported on this platform")

func AutostartEnabled() bool         { return false }
func SetAutostart(bool) error        { return errIntegrationUnsupported }
func RegisterHandlers() error        { return nil }
func IsDefaultMail() bool            { return false }
func IsDefaultCalendar() bool        { return false }
func OpenDefaultAppsSettings() error { return errIntegrationUnsupported }
func UnregisterHandlers() error      { return nil }

const IntegrationSupported = false
