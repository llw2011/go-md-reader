param(
    [switch]$Unregister
)

$exePath = Join-Path $PSScriptRoot "go-md-reader.exe"

if (-not (Test-Path $exePath)) {
    Write-Host "错误: 未找到 $exePath，请先运行 .\build.ps1 编译程序。" -ForegroundColor Red
    exit 1
}

$progId = "GoMarkdownReader.md"
$progIdKey = "HKCU:\Software\Classes\$progId"

if ($Unregister) {
    Write-Host "正在移除 Go Markdown Reader 文件关联..." -ForegroundColor Yellow
    Remove-Item -Path $progIdKey -Recurse -Force -ErrorAction SilentlyContinue
    
    $mdKey = Get-ItemProperty "HKCU:\Software\Classes\.md" -ErrorAction SilentlyContinue
    if ($mdKey -and $mdKey."(default)" -eq $progId) {
        Remove-Item -Path "HKCU:\Software\Classes\.md" -Recurse -Force -ErrorAction SilentlyContinue
    }

    $mdownKey = Get-ItemProperty "HKCU:\Software\Classes\.markdown" -ErrorAction SilentlyContinue
    if ($mdownKey -and $mdownKey."(default)" -eq $progId) {
        Remove-Item -Path "HKCU:\Software\Classes\.markdown" -Recurse -Force -ErrorAction SilentlyContinue
    }
    
    Write-Host "已成功解除 .md 与 .markdown 的关联。" -ForegroundColor Green
    exit 0
}

Write-Host "正在将 Go Markdown Reader 设置为系统默认 .md 打开程序..." -ForegroundColor Cyan

# 1. 注册 ProgID
New-Item -Path $progIdKey -Force | Out-Null
Set-ItemProperty -Path $progIdKey -Name "(default)" -Value "Markdown 文本文档"
New-Item -Path "$progIdKey\DefaultIcon" -Force | Out-Null
Set-ItemProperty -Path "$progIdKey\DefaultIcon" -Name "(default)" -Value "`"$exePath`",0"

$cmdKey = "$progIdKey\shell\open\command"
New-Item -Path $cmdKey -Force | Out-Null
Set-ItemProperty -Path $cmdKey -Name "(default)" -Value "`"$exePath`" `"%1`""

# 2. 关联 .md 和 .markdown 扩展名
$exts = @(".md", ".markdown")
foreach ($ext in $exts) {
    $extKey = "HKCU:\Software\Classes\$ext"
    New-Item -Path $extKey -Force | Out-Null
    Set-ItemProperty -Path $extKey -Name "(default)" -Value $progId
    
    $fileExtPath = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\$ext\OpenWithProgids"
    if (Test-Path $fileExtPath) {
        Set-ItemProperty -Path $fileExtPath -Name $progId -Value ([byte[]]@()) -Type None
    }
}

# 3. 刷新 Windows Shell 缓存
$code = @"
using System;
using System.Runtime.InteropServices;
public class ShellNotifier {
    [DllImport("shell32.dll")]
    public static extern void SHChangeNotify(int wEventId, uint uFlags, IntPtr dwItem1, IntPtr dwItem2);
}
"@
Add-Type -TypeDefinition $code -ErrorAction SilentlyContinue
[ShellNotifier]::SHChangeNotify(0x08000000, 0, [IntPtr]::Zero, [IntPtr]::Zero) # SHCNE_ASSOCCHANGED

Write-Host "设置成功！现在双击任意 .md 或 .markdown 文件即可通过您的自研 Go 阅读器打开。" -ForegroundColor Green
