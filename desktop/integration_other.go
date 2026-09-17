//go:build !windows && !darwin

package main

import "errors"

// 非 Windows 平台暂不支持自启与默认应用登记; macOS 需要 LaunchAgent 与 LSSetDefaultHandler(cgo)。

var errIntegrationUnsupported = errors.New("2170 not supported on this platform")

func autostartEnabled() bool         { return false }
func setAutostart(bool) error        { return errIntegrationUnsupported }
func registerHandlers() error        { return nil }
func isDefaultMail() bool            { return false }
func isDefaultCalendar() bool        { return false }
func openDefaultAppsSettings() error { return errIntegrationUnsupported }

const integrationSupported = false
