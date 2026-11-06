# CFSTX 发布构建脚本
# 用法：
#   .\release.ps1              # 使用 version.txt 中的版本号
#   .\release.ps1 -Version 1.0.1   # 指定版本号
#
# 产出：
#   dist\CFSTX-Setup-<ver>.exe      安装包
#   dist\CFSTX-<ver>-portable.zip   免安装版

param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

# 1. 确定版本号（去掉前缀 v）
if (-not $Version) {
    $Version = (Get-Content "version.txt" -Raw).Trim()
}
$verNum = $Version -replace '^v', ''

Write-Host "[1/6] 版本号: v$verNum" -ForegroundColor Cyan
[System.IO.File]::WriteAllText((Join-Path $root "version.txt"), "v$verNum`n", $utf8NoBom)

# 2. 同步 installer.iss 中的版本号
$issPath = Join-Path $root "installer.iss"
$iss = [System.IO.File]::ReadAllText($issPath)
$iss = $iss -replace '#define MyAppVersion ".*"', "#define MyAppVersion `"$verNum`""
[System.IO.File]::WriteAllText($issPath, $iss, $utf8NoBom)

# 3. 构建前端
Write-Host "[2/6] 构建前端..." -ForegroundColor Cyan
# esbuild 在系统 TEMP 目录偶尔会因权限问题删除临时文件失败（Access is denied），
# 改用项目内的临时目录，避免构建中断
$buildTemp = Join-Path $root "build\tmp"
New-Item -ItemType Directory -Path $buildTemp -Force | Out-Null
$env:TEMP = $buildTemp
$env:TMP = $buildTemp
Push-Location frontend
npm run build
New-Item -ItemType File "dist\.gitkeep" -Force | Out-Null
Pop-Location

# 4. 编译 exe（GUI 子系统）
Write-Host "[3/6] 编译 cfstx.exe..." -ForegroundColor Cyan
New-Item -ItemType Directory -Path "build\bin" -Force | Out-Null
go build -ldflags "-H windowsgui -X gitee.com/liliaic/cfstx/version.Current=v$verNum" -o "build\bin\cfstx.exe" .
icacls "build\bin\cfstx.exe" /setintegritylevel Medium | Out-Null

# 校验：exe 的时间戳必须晚于所有源码，否则说明打包了旧代码
# （v1.0.0 就因为提交源码后没重新编译，导致安装包里缺少更新接口）
$exeTime = (Get-Item "build\bin\cfstx.exe").LastWriteTime
$newestSource = Get-ChildItem -Recurse -File |
    Where-Object {
        $_.Extension -in '.go', '.vue', '.js' -and
        $_.FullName -notmatch '\\node_modules\\' -and
        $_.FullName -notmatch '\\build\\' -and
        $_.FullName -notmatch '\\frontend\\dist\\'
    } |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 1
if ($newestSource -and $newestSource.LastWriteTime -gt $exeTime) {
    throw "编译产物比源码旧：$($newestSource.Name) 修改于 $($newestSource.LastWriteTime)，而 exe 生成于 $exeTime。请检查编译步骤。"
}
Write-Host "  校验通过：exe 时间戳晚于所有源码" -ForegroundColor Green

# 5. 编译安装包
Write-Host "[4/6] 编译安装包..." -ForegroundColor Cyan
$iscc = "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
if (-not (Test-Path $iscc)) {
    $iscc = "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
}
if (-not (Test-Path $iscc)) {
    throw "未找到 Inno Setup 编译器 ISCC.exe，请先安装 Inno Setup 6"
}
& $iscc "installer.iss" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "安装包编译失败" }

# 在低完整性沙箱目录中构建的安装包会继承 Low IL，
# 运行时无法写入 %TEMP% 会报「Error 5: 拒绝访问」，统一提升为 Medium。
icacls "dist\CFSTX-Setup-$verNum.exe" /setintegritylevel Medium | Out-Null

# 6. 打包免安装 zip
Write-Host "[5/6] 打包免安装版..." -ForegroundColor Cyan
New-Item -ItemType Directory -Path "dist" -Force | Out-Null
$zipDir = "dist\CFSTX-$verNum"
if (Test-Path $zipDir) { Remove-Item $zipDir -Recurse -Force }
New-Item -ItemType Directory -Path $zipDir -Force | Out-Null
Copy-Item "build\bin\cfstx.exe" $zipDir
Copy-Item "ip.txt", "ipv6.txt", "LICENSE", "version.txt" $zipDir
Copy-Item "script" $zipDir -Recurse
Compress-Archive -Path "$zipDir\*" -DestinationPath "dist\CFSTX-$verNum-portable.zip" -Force
Remove-Item $zipDir -Recurse -Force
icacls "dist\CFSTX-$verNum-portable.zip" /setintegritylevel Medium | Out-Null

# 7. 输出结果
Write-Host "[6/6] 完成" -ForegroundColor Green
Write-Host ""
Get-ChildItem "dist" -File | Where-Object { $_.Name -match "CFSTX" } |
    Select-Object Name, @{N = 'SizeMB'; E = { [math]::Round($_.Length / 1MB, 2) } }, LastWriteTime |
    Format-Table -AutoSize

Write-Host "下一步：在 Gitee 创建 Release（tag: v$verNum），上传上述两个文件。" -ForegroundColor Yellow
