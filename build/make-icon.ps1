# 生成 CFSTX 多尺寸图标
#
# 用法：
#   .\build\make-icon.ps1
#
# 产出：
#   assets\cfstx.ico          多尺寸图标（16/24/32/48/64/128/256）
#   rsrc_windows_amd64.syso   嵌入 exe 的资源文件
#
# 设计：紫色渐变圆角底 + 白色 C 形 + 黄色闪电
# 注意：PowerShell 函数返回 byte[] 时必须用 ,$result 阻止数组展开，
#       否则 BinaryWriter.Write 只会写入一个字节。

Add-Type -AssemblyName System.Drawing

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

function New-CFSTXIconBitmap([int]$size) {
    $bmp = New-Object System.Drawing.Bitmap $size, $size
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $g.Clear([System.Drawing.Color]::Transparent)

    $s = $size / 256.0

    # 圆角矩形背景
    $radius = [int](56 * $s); $d = $radius * 2
    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $path.AddArc(0, 0, $d, $d, 180, 90)
    $path.AddArc($size - $d, 0, $d, $d, 270, 90)
    $path.AddArc($size - $d, $size - $d, $d, $d, 0, 90)
    $path.AddArc(0, $size - $d, $d, $d, 90, 90)
    $path.CloseFigure()

    $brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
        (New-Object System.Drawing.Point 0, 0),
        (New-Object System.Drawing.Point $size, $size),
        [System.Drawing.Color]::FromArgb(255, 139, 92, 246),
        [System.Drawing.Color]::FromArgb(255, 79, 70, 229))
    $g.FillPath($brush, $path)

    # 白色 C 形
    $pen = New-Object System.Drawing.Pen ([System.Drawing.Color]::White), ([float](38 * $s))
    $pen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
    $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $g.DrawArc($pen, [float](64*$s), [float](64*$s), [float](128*$s), [float](128*$s), 52, 256)

    # 黄色闪电
    $pts = @(
        [System.Drawing.PointF]::new(144*$s, 80*$s),
        [System.Drawing.PointF]::new(112*$s, 134*$s),
        [System.Drawing.PointF]::new(128*$s, 134*$s),
        [System.Drawing.PointF]::new(118*$s, 178*$s),
        [System.Drawing.PointF]::new(154*$s, 122*$s),
        [System.Drawing.PointF]::new(136*$s, 122*$s))
    $yellow = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 253, 224, 71))
    $g.FillPolygon($yellow, $pts)

    $g.Dispose()
    return $bmp
}

function Get-CFSTXIconPng([int]$size) {
    $bmp = New-CFSTXIconBitmap $size
    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    $result = $ms.ToArray()
    $ms.Dispose()
    return ,$result
}

function Write-CFSTXIcon {
    param([int[]]$Sizes, [string]$OutPath)

    $entries = New-Object System.Collections.ArrayList
    foreach ($sz in $Sizes) {
        [void]$entries.Add([pscustomobject]@{ Size = $sz; Data = (Get-CFSTXIconPng $sz) })
    }

    $ms = New-Object System.IO.MemoryStream
    $bw = New-Object System.IO.BinaryWriter($ms)
    $bw.Write([UInt16]0); $bw.Write([UInt16]1); $bw.Write([UInt16]$entries.Count)

    $offset = 6 + 16 * $entries.Count
    foreach ($e in $entries) {
        $dim = if ($e.Size -ge 256) { 0 } else { $e.Size }
        $bw.Write([Byte]$dim); $bw.Write([Byte]$dim)
        $bw.Write([Byte]0); $bw.Write([Byte]0)
        $bw.Write([UInt16]1); $bw.Write([UInt16]32)
        $bw.Write([UInt32]$e.Data.Length)
        $bw.Write([UInt32]$offset)
        $offset += $e.Data.Length
    }
    foreach ($e in $entries) { $bw.Write($e.Data) }
    $bw.Flush()

    New-Item -ItemType Directory -Path (Split-Path $OutPath -Parent) -Force | Out-Null
    [System.IO.File]::WriteAllBytes($OutPath, $ms.ToArray())
    $bw.Close(); $ms.Close()
    Write-Host "已生成 $OutPath ($((Get-Item $OutPath).Length) 字节)" -ForegroundColor Green
}

Write-CFSTXIcon -Sizes @(16,24,32,48,64,128,256) -OutPath (Join-Path $root "assets\cfstx.ico")

# 重新生成 syso，让 exe 图标生效
$rsrc = Join-Path (go env GOPATH) "bin\rsrc.exe"
if (Test-Path $rsrc) {
    & $rsrc -ico "assets\cfstx.ico" -o "rsrc_windows_amd64.syso"
    Write-Host "已生成 rsrc_windows_amd64.syso" -ForegroundColor Green
} else {
    Write-Host "未找到 rsrc.exe，跳过 syso 生成" -ForegroundColor Yellow
    Write-Host "安装：go install github.com/akavel/rsrc@latest" -ForegroundColor Yellow
}
