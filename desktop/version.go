package main

// version 在构建时由 -ldflags "-X main.version=vX.Y.Z" 注入, 取自 git tag。
// 本地未注入时为 dev, 自更新据此跳过版本比较。
var version = "dev"

// GetVersion 返回当前版本, 供界面显示与自更新比较。
func (a *App) GetVersion() string {
	return version
}
