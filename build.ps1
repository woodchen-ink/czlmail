# CZL Mail 本地构建: 出 exe 与 NSIS 安装包, 产物在 desktop\build\bin。
#
#   .\build.ps1            版本取最近的 git tag (如 v0.0.2)
#   .\build.ps1 v0.1.0     指定版本
#
# 不要给 wails build 加 -v 2: 会把整个进程环境变量(含密钥)打进输出。
param([string]$Version)

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot 'desktop')

if (-not $Version) {
    $Version = (git describe --tags --abbrev=0 2>$null)
    if (-not $Version) { $Version = 'v0.0.0-dev' }
}
# 安装包版本号来自 wails.json 的 productVersion, 必须是纯数字的 x.y.z。
$productVersion = ($Version -replace '^v', '') -replace '[-+].*$', ''

if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
    $env:PATH += ';C:\Program Files (x86)\NSIS'
}
if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
    throw '找不到 makensis, 请安装 NSIS: https://nsis.sourceforge.io/Download'
}
if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
    throw '找不到 wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0'
}

Write-Host "构建版本 $Version (安装包版本 $productVersion)"

# 按文本替换而不是 JSON 往返, 保留 wails.json 原有格式。
$wailsJson = Join-Path (Get-Location) 'wails.json'
$text = [IO.File]::ReadAllText($wailsJson)
$updated = $text -replace '"productVersion":\s*"[^"]*"', "`"productVersion`": `"$productVersion`""
if ($updated -ne $text) { [IO.File]::WriteAllText($wailsJson, $updated) }

# 不用 -clean: 输出目录被资源管理器或正在运行的程序占用时, 清空整个目录会失败。
wails build -nsis -ldflags "-X main.version=$Version"
if ($LASTEXITCODE -ne 0) { throw "构建失败 (exit $LASTEXITCODE)" }

Write-Host ''
Write-Host '完成:'
Get-ChildItem build\bin | Format-Table Name, Length, LastWriteTime -AutoSize
