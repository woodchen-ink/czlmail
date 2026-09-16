@echo off
rem CZL Mail 本地构建入口, 实际逻辑在 build.ps1。
rem   build.bat            版本取最近的 git tag
rem   build.bat v0.1.0     指定版本
chcp 65001 >nul
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" %*
if errorlevel 1 (
  echo.
  echo 构建失败
  if "%~1"=="" pause
  exit /b 1
)
if "%~1"=="" pause
