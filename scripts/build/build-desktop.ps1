param([switch]$DebugBuild)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot '../dev/env.ps1')
Push-Location (Join-Path $hopsendRoot 'apps/desktop')
try {
    Push-Location frontend
    try {
        & pnpm install --frozen-lockfile
        if($LASTEXITCODE -ne 0){throw '前端依赖安装失败'}
        & pnpm build
        if($LASTEXITCODE -ne 0){throw '前端检查或构建失败'}
    } finally {Pop-Location}
    $hopBuildFlags=@('build','-clean','-o','hopsend.exe')
    if($DebugBuild){$hopBuildFlags+='-debug'}
    & wails @hopBuildFlags
    if($LASTEXITCODE -ne 0){throw 'Wails 桌面构建失败'}
    $hopDesktopOut=Join-Path $hopsendRoot 'dist/desktop'
    New-Item -ItemType Directory -Path $hopDesktopOut -Force|Out-Null
    Copy-Item -LiteralPath 'build/bin/hopsend.exe' -Destination (Join-Path $hopDesktopOut 'hopsend.exe') -Force
    & go build -trimpath -o (Join-Path $hopDesktopOut 'hopsend-agent.exe') ../agent
    if($LASTEXITCODE -ne 0){throw '配套 Agent 构建失败'}
} finally {Pop-Location}
