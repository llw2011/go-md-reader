# Go Markdown Reader & 独立安装包构建脚本
$ErrorActionPreference = "Stop"

Set-Location $PSScriptRoot

Write-Host "[1/4] 正在生成高清程序图标与 Windows 资源文件 (.syso)..." -ForegroundColor Cyan
go run ./cmd/genicon

Write-Host "[2/4] 正在编译主程序 go-md-reader.exe (GUI 模式)..." -ForegroundColor Cyan
$appOutput = Join-Path $PSScriptRoot "go-md-reader.exe"
go build -ldflags="-H windowsgui -s -w" -o $appOutput .

Write-Host "[3/4] 正在打包安装载荷并编译图形化安装包 GoMarkdownReader-Setup.exe..." -ForegroundColor Cyan
$payloadDir = Join-Path $PSScriptRoot "cmd\installer\payload"
New-Item -ItemType Directory -Force -Path $payloadDir | Out-Null
Copy-Item -Path $appOutput -Destination (Join-Path $payloadDir "go-md-reader.exe") -Force
Copy-Item -Path (Join-Path $PSScriptRoot "sample.md") -Destination (Join-Path $payloadDir "sample.md") -Force

$setupOutput = Join-Path $PSScriptRoot "GoMarkdownReader-Setup.exe"
go build -ldflags="-H windowsgui -s -w" -o $setupOutput ./cmd/installer

$desktopSetup = Join-Path ([Environment]::GetFolderPath("Desktop")) "GoMarkdownReader-Setup.exe"
Copy-Item -Path $setupOutput -Destination $desktopSetup -Force

$appSize = (Get-Item $appOutput).Length / 1MB
$setupSize = (Get-Item $setupOutput).Length / 1MB

Write-Host "`n================ 构建成功 ================" -ForegroundColor Green
Write-Host ("主程序路径   : {0} ({1:N2} MB)" -f $appOutput, $appSize)
Write-Host ("安装包路径   : {0} ({1:N2} MB)" -f $setupOutput, $setupSize)
Write-Host ("桌面快捷副本 : {0}" -f $desktopSetup)
