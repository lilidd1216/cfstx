# CFSTX 构建脚本
# 用法: .\build.ps1
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

Write-Host "[1/3] 构建前端..." -ForegroundColor Cyan
Push-Location frontend
npm run build
# vite 的 emptyOutDir 会清空 dist，这里补回占位文件，
# 保证 //go:embed all:frontend/dist 在干净仓库中也能编译
New-Item -ItemType File "dist\.gitkeep" -Force | Out-Null
Pop-Location

Write-Host "[2/3] 编译 Go 程序..." -ForegroundColor Cyan
New-Item -ItemType Directory -Path "build\bin" -Force | Out-Null
# -H windowsgui：生成 GUI 子系统程序，双击时不会弹出黑色控制台窗口。
# 命令行模式下由 console_windows.go 中的 AttachConsole 重新附加到父控制台。
go build -ldflags "-H windowsgui" -o "build\bin\cfstx.exe" .

# 如果项目位于低完整性沙箱目录（如 Codex Sandbox 的 Low Mandatory Level），
# 构建出的 exe 会继承 Low IL，导致 WebView2 无法创建子进程（0x8000FFFF）。
# 这里统一提升到 Medium，正常目录下执行该命令也无副作用。
Write-Host "[3/3] 修正 exe 完整性级别..." -ForegroundColor Cyan
icacls "build\bin\cfstx.exe" /setintegritylevel Medium | Out-Null

$size = [math]::Round((Get-Item "build\bin\cfstx.exe").Length / 1MB, 2)
Write-Host "构建完成: build\bin\cfstx.exe ($size MB)" -ForegroundColor Green
