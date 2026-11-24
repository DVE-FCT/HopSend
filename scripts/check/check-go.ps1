param([switch]$StrictDoctor)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../dev/env.ps1')
Push-Location $hopsendRoot
try {
    & go version
    if ($LASTEXITCODE -ne 0) { throw 'Go 不可用' }
    if (Test-Path -LiteralPath apps/desktop/frontend/package.json) {
        Push-Location apps/desktop/frontend
        try {
            & pnpm install --frozen-lockfile
            if ($LASTEXITCODE -ne 0) { throw '前端依赖失败' }
            & pnpm build
            if ($LASTEXITCODE -ne 0) { throw '前端 TypeScript 或构建失败' }
        } finally { Pop-Location }
    }
    & node --test scripts/check/package-flow.test.mjs
    if ($LASTEXITCODE -ne 0) { throw '配对向导流程检查失败' }
    $unformatted = @(& gofmt -l apps internal configs contracts tests)
    if ($LASTEXITCODE -ne 0 -or $unformatted.Count -gt 0) { throw ('Go 格式检查失败: ' + ($unformatted -join ', ')) }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet 失败' }
    & go test -count=1 -timeout=240s ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go 测试失败' }
    New-Item -ItemType Directory -Path dist/cli -Force | Out-Null
    & go build -trimpath -o dist/cli/hopsend-cli.exe ./apps/cli
    if ($LASTEXITCODE -ne 0) { throw 'CLI 构建失败' }
    if (Test-Path -LiteralPath apps/agent/main.go) {
        New-Item -ItemType Directory -Path dist/agent -Force | Out-Null
        & go build -trimpath -o dist/agent/hopsend-agent.exe ./apps/agent
        if ($LASTEXITCODE -ne 0) { throw 'Agent 构建失败' }
    }
    & ./dist/cli/hopsend-cli.exe version
    if ($LASTEXITCODE -ne 0) { throw 'CLI 启动失败' }
    if ($StrictDoctor) {
        & ./dist/cli/hopsend-cli.exe doctor --strict
        if ($LASTEXITCODE -ne 0) { throw '固定工具链诊断失败' }
    }
    & git diff --check
    if ($LASTEXITCODE -ne 0) { throw 'Git 差异格式失败' }
} finally { Pop-Location }
