# CFSTX 双平台发布脚本
#
# 用法：
#   .\publish.ps1                    # 构建并发布当前版本到 Gitee + GitHub
#   .\publish.ps1 -SkipBuild         # 跳过构建，用 dist\ 里已有的产物发布
#   .\publish.ps1 -GiteeOnly         # 只发 Gitee
#   .\publish.ps1 -GitHubOnly        # 只发 GitHub
#   .\publish.ps1 -DryRun            # 只检查，不上传
#
# Token 来源（按顺序）：
#   1. 环境变量 GITEE_TOKEN / GITHUB_TOKEN
#   2. %USERPROFILE%\.claude.json 里 MCP 配置的 Authorization
#
# 前置：
#   - 版本号已更新（version.txt、version/version.go）
#   - 代码已提交并推送（两个平台都推）
#   - tag 已创建：git tag -a vX.Y.Z -m "..." && git push origin vX.Y.Z

param(
    [string]$Version = "",
    [switch]$SkipBuild,
    [switch]$GiteeOnly,
    [switch]$GitHubOnly,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$GITEE_OWNER = "liliaic"
$GITEE_REPO  = "cfstx"
$GH_OWNER    = "lilidd1216"
$GH_REPO     = "cfstx"

# ---------------------------------------------------------------- 版本号

if (-not $Version) {
    $Version = (Get-Content "version.txt" -Raw).Trim()
}
$verNum = $Version -replace '^v', ''
$tag = "v$verNum"
Write-Host "[1/5] 版本: $tag" -ForegroundColor Cyan

# ---------------------------------------------------------------- 构建

if (-not $SkipBuild) {
    Write-Host "[2/5] 构建发布产物..." -ForegroundColor Cyan
    & .\release.ps1 -Version $verNum *> "build\tmp\release.log"
    if ($LASTEXITCODE -ne 0) { throw "release.ps1 失败，详见 build\tmp\release.log" }
} else {
    Write-Host "[2/5] 跳过构建" -ForegroundColor DarkGray
}

$setup = "dist\CFSTX-Setup-$verNum.exe"
$portable = "dist\CFSTX-$verNum-portable.zip"
foreach ($f in @($setup, $portable)) {
    if (-not (Test-Path $f)) { throw "找不到产物: $f" }
}

# ---------------------------------------------------------------- 更新说明
# 从 CHANGELOG.md 里提取当前版本的段落（### vX.Y.Z 到下一个 ### 之间）

Write-Host "[3/5] 提取更新说明..." -ForegroundColor Cyan
$body = ""
if (Test-Path "CHANGELOG.md") {
    $lines = Get-Content "CHANGELOG.md"
    $capture = $false
    $buf = @()
    foreach ($line in $lines) {
        if ($line -match '^###\s') {
            if ($capture) { break }
            if ($line -match [regex]::Escape($tag)) { $capture = $true; continue }
        }
        if ($capture) { $buf += $line }
    }
    $body = ($buf -join "`n").Trim()
}
if (-not $body) {
    $body = "## $tag`n`n详见 [CHANGELOG.md](https://gitee.com/$GITEE_OWNER/$GITEE_REPO/blob/master/CHANGELOG.md)"
    Write-Host "      CHANGELOG 里没找到 $tag 的段落，使用默认说明" -ForegroundColor Yellow
}

# 加上下载表格
$body += @"

---

### 下载

| 版本 | 文件 |
|---|---|
| 安装版（推荐） | ``CFSTX-Setup-$verNum.exe`` |
| 免安装版 | ``CFSTX-$verNum-portable.zip`` |

> 未购买代码签名证书，首次运行时 Windows SmartScreen 可能提示「无法验证发布者」，
> 点击「更多信息」→「仍要运行」即可。
"@

# ---------------------------------------------------------------- Token

function Get-Token([string]$envName, [string]$mcpKey) {
    $v = [Environment]::GetEnvironmentVariable($envName)
    if ($v) { return $v.Trim() }
    $cfgPath = Join-Path $env:USERPROFILE ".claude.json"
    if (-not (Test-Path $cfgPath)) { return "" }
    try {
        $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json -AsHashtable
        $auth = $cfg.mcpServers[$mcpKey].headers['Authorization']
        if ($auth) { return ($auth -replace '^(Bearer|token)\s+', '').Trim() }
    } catch { }
    return ""
}

# ---------------------------------------------------------------- Gitee

function Publish-Gitee([string]$token) {
    $h = @{ "Content-Type" = "application/json;charset=UTF-8" }
    $base = "https://gitee.com/api/v5/repos/$GITEE_OWNER/$GITEE_REPO"

    # 已存在就复用
    $rel = $null
    try {
        $rel = Invoke-RestMethod -Uri "$base/releases/tags/$tag`?access_token=$token" -TimeoutSec 60
    } catch {
        Write-Host "      Release 不存在，创建中..." -ForegroundColor DarkGray
    }

    if (-not $rel) {
        $payload = @{
            tag_name         = $tag
            name           = "$tag"
            body           = $body
            target_commitish = "master"
        } | ConvertTo-Json
        $rel = Invoke-RestMethod -Method Post -Uri "$base/releases?access_token=$token" `
            -ContentType "application/json;charset=UTF-8" -Body ([System.Text.Encoding]::UTF8.GetBytes($payload)) -TimeoutSec 60
        Write-Host "      已创建 Release #$($rel.id)" -ForegroundColor Green
    } else {
        Write-Host "      Release 已存在 #$($rel.id)" -ForegroundColor DarkGray
    }

    # 已有同名附件就跳过
    $existing = @()
    try {
        $existing = @(Invoke-RestMethod -Uri "$base/releases/$($rel.id)/attach_files?access_token=$token" -TimeoutSec 60)
    } catch { }

    foreach ($f in @($setup, $portable)) {
        $name = Split-Path $f -Leaf
        if ($existing.name -contains $name) {
            Write-Host "      跳过已存在的附件 $name" -ForegroundColor DarkGray
            continue
        }
        $r = Invoke-RestMethod -Method Post -Uri "$base/releases/$($rel.id)/attach_files?access_token=$token" `
            -Form @{ file = Get-Item $f } -TimeoutSec 600
        Write-Host "      已上传 $($r.name) ($([math]::Round($r.size/1MB,2)) MB)" -ForegroundColor Green
    }

    return "https://gitee.com/$GITEE_OWNER/$GITEE_REPO/releases/tag/$tag"
}

# ---------------------------------------------------------------- GitHub

function Publish-GitHub([string]$token) {
    $auth = @{
        "Authorization"        = "Bearer $token"
        "Accept"               = "application/vnd.github+json"
        "X-GitHub-Api-Version" = "2022-11-28"
        "User-Agent"           = "CFSTX-Release"
    }
    $base = "https://api.github.com/repos/$GH_OWNER/$GH_REPO"

    $rel = $null
    try {
        $rel = Invoke-RestMethod -Uri "$base/releases/tags/$tag" -Headers $auth -TimeoutSec 60
    } catch {
        Write-Host "      Release 不存在，创建中..." -ForegroundColor DarkGray
    }

    if (-not $rel) {
        $payload = @{
            tag_name = $tag
            name     = $tag
            body     = $body
            draft    = $false
        } | ConvertTo-Json
        $rel = Invoke-RestMethod -Method Post -Uri "$base/releases" -Headers $auth `
            -ContentType "application/json" -Body ([System.Text.Encoding]::UTF8.GetBytes($payload)) -TimeoutSec 60
        Write-Host "      已创建 Release #$($rel.id)" -ForegroundColor Green
    } else {
        Write-Host "      Release 已存在 #$($rel.id)" -ForegroundColor DarkGray
    }

    $existing = @()
    try {
        $existing = @(Invoke-RestMethod -Uri "$base/releases/$($rel.id)/assets" -Headers $auth -TimeoutSec 60)
    } catch { }

    foreach ($f in @($setup, $portable)) {
        $name = Split-Path $f -Leaf
        if ($existing.name -contains $name) {
            Write-Host "      跳过已存在的附件 $name" -ForegroundColor DarkGray
            continue
        }
        $bytes = [System.IO.File]::ReadAllBytes((Resolve-Path $f))
        $up = "https://uploads.github.com/repos/$GH_OWNER/$GH_REPO/releases/$($rel.id)/assets?name=$name"
        $r = Invoke-RestMethod -Method Post -Uri $up -Headers $auth `
            -ContentType "application/octet-stream" -Body $bytes -TimeoutSec 900
        Write-Host "      已上传 $($r.name) ($([math]::Round($r.size/1MB,2)) MB)" -ForegroundColor Green
    }

    return "https://github.com/$GH_OWNER/$GH_REPO/releases/tag/$tag"
}

# ---------------------------------------------------------------- 执行

Write-Host "[4/5] 发布..." -ForegroundColor Cyan

if ($DryRun) {
    Write-Host "      [DryRun] 将发布 $tag 到：" -ForegroundColor Yellow
    if (-not $GitHubOnly) { Write-Host "        Gitee  $GITEE_OWNER/$GITEE_REPO" }
    if (-not $GiteeOnly)  { Write-Host "        GitHub $GH_OWNER/$GH_REPO" }
    Write-Host "      附件：$setup、$portable"
    Write-Host "      说明长度：$($body.Length) 字符"
    return
}

$giteeURL = ""
$githubURL = ""

if (-not $GitHubOnly) {
    $t = Get-Token "GITEE_TOKEN" "gitee"
    if (-not $t) { Write-Host "      跳过 Gitee：没有 token" -ForegroundColor Yellow }
    else {
        try {
            Write-Host "      → Gitee" -ForegroundColor Cyan
            $giteeURL = Publish-Gitee $t
        } catch {
            Write-Host "      Gitee 失败: $($_.Exception.Message -replace [regex]::Escape($t), '***')" -ForegroundColor Red
        }
    }
}

if (-not $GiteeOnly) {
    $t = Get-Token "GITHUB_TOKEN" "github"
    if (-not $t) { Write-Host "      跳过 GitHub：没有 token" -ForegroundColor Yellow }
    else {
        try {
            Write-Host "      → GitHub" -ForegroundColor Cyan
            $githubURL = Publish-GitHub $t
        } catch {
            Write-Host "      GitHub 失败: $($_.Exception.Message -replace [regex]::Escape($t), '***')" -ForegroundColor Red
        }
    }
}

# ---------------------------------------------------------------- 结果

Write-Host "[5/5] 完成" -ForegroundColor Cyan
if ($giteeURL)  { Write-Host "  Gitee : $giteeURL" -ForegroundColor Green }
if ($githubURL) { Write-Host "  GitHub: $githubURL" -ForegroundColor Green }
if (-not $giteeURL -and -not $githubURL) {
    Write-Host "  没有任何平台成功，检查 token 和网络" -ForegroundColor Red
    exit 1
}
