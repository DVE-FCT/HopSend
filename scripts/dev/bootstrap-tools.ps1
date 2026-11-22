param([switch]$UseSystemProxy)
$ErrorActionPreference = 'Stop'
$bootstrapRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$pins = Get-Content -LiteralPath (Join-Path $bootstrapRoot 'configs/toolchain.json') -Raw | ConvertFrom-Json
$toolDir = Join-Path $bootstrapRoot 'work/.tools'
$localGo = Join-Path $toolDir 'go/bin/go.exe'
New-Item -ItemType Directory -Path $toolDir -Force | Out-Null
if (-not (Test-Path -LiteralPath $localGo)) {
    if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw '此引导脚本目前只支持 Windows amd64；其他平台请手动安装固定版本工具链。' }
    $archive = Join-Path $toolDir ('go' + $pins.go + '.windows-amd64.zip')
    Invoke-WebRequest -UseBasicParsing -Uri ('https://go.dev/dl/go' + $pins.go + '.windows-amd64.zip') -OutFile $archive -TimeoutSec 180
    $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $pins.go_windows_amd64_sha256) { throw 'Go 下载包 SHA-256 不匹配，停止安装。' }
    if (Test-Path -LiteralPath (Join-Path $toolDir 'go')) { throw 'Go 目标目录已存在但不完整，请检查后另行处理。' }
    Expand-Archive -LiteralPath $archive -DestinationPath $toolDir
}
. (Join-Path $PSScriptRoot 'env.ps1')
if ($UseSystemProxy -and -not $env:HTTPS_PROXY) {
    $systemProxy = [System.Net.WebRequest]::GetSystemWebProxy().GetProxy([uri]'https://proxy.golang.org')
    if ($systemProxy.Host -ne 'proxy.golang.org') {
        $env:HTTPS_PROXY = $systemProxy.GetLeftPart([System.UriPartial]::Authority)
    }
}
if ((& go version) -notmatch ('go' + [regex]::Escape($pins.go) + '\s')) { throw 'Go 版本与契约不符。' }
$env:GOBIN = Join-Path $toolDir 'bin'
New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
& go install ('github.com/wailsapp/wails/v2/cmd/wails@v' + $pins.wails)
if ($LASTEXITCODE -ne 0) { throw 'Wails CLI 安装失败。' }
Write-Output '项目本地 Go 与 Wails CLI 已准备；Node 和 pnpm 请按 configs/toolchain.json 安装。'
Write-Output '新终端先执行 . ./scripts/dev/env.ps1，再运行 CLI doctor --strict。'
