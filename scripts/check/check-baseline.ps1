param([string]$ReportPath = '', [switch]$AllowMainReadOnly)

$ErrorActionPreference = 'Stop'
$baselineRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$baselineChecks = [Collections.Generic.List[object]]::new()

function Add-BaselineCheck([string]$Name, [bool]$Passed, [string]$Detail) {
    $baselineChecks.Add([pscustomobject]@{ name = $Name; passed = $Passed; detail = $Detail })
}

Push-Location $baselineRoot
try {
    $branch = (& git branch --show-current).Trim()
    $remote = (& git remote get-url origin).Trim()
    $branchAllowed = $branch -like 'test/*' -or ($AllowMainReadOnly -and $branch -eq 'main')
    Add-BaselineCheck '验收分支' $branchAllowed $branch
    Add-BaselineCheck '远端地址' ($remote -eq 'git@github.com:DVE-FCT/HopSend.git') $remote

    $required = @('apps/desktop/frontend/src/views','apps/agent','apps/cli','internal/domain',
        'internal/engine','internal/transport/sftp','internal/storage/sqlite/migrations',
        'contracts/control/v1','contracts/manifest/v1','contracts/events/v1',
        'tests/integration','tests/e2e','packaging/windows/service','docs/validation')
    $missing = @($required | Where-Object { -not (Test-Path -LiteralPath $_ -PathType Container) })
    Add-BaselineCheck '工程分区' ($missing.Count -eq 0) ($missing -join ', ')

    $linkProblems = [Collections.Generic.List[string]]::new()
    $markdownFiles = @(Get-ChildItem -LiteralPath $baselineRoot -Recurse -Force -File -Filter '*.md' |
        Where-Object { $_.FullName -notmatch '[\\/](\.git|work|node_modules)[\\/]' })
    foreach ($file in $markdownFiles) {
        $content = [IO.File]::ReadAllText($file.FullName)
        if (([regex]::Matches($content, '```')).Count % 2 -ne 0) {
            $linkProblems.Add($file.Name + ': 代码围栏不成对')
        }
        foreach ($match in [regex]::Matches($content, '!?\[[^\]]*\]\(([^)]+)\)')) {
            $link = $match.Groups[1].Value
            if ($link -match '^[a-zA-Z][a-zA-Z0-9+.-]*://' -or $link.StartsWith('#')) { continue }
            $relative = $link.Split('#')[0]
            if (-not (Test-Path -LiteralPath (Join-Path $file.DirectoryName $relative))) {
                $linkProblems.Add($file.Name + ': ' + $relative)
            }
        }
    }
    Add-BaselineCheck '文档链接与围栏' ($linkProblems.Count -eq 0) ($linkProblems -join '; ')

    $defaults = Get-Content -LiteralPath 'configs/examples/design-defaults.json' -Raw | ConvertFrom-Json
    $defaultsValid = $defaults.document_kind -eq 'design_defaults_not_runtime_config' -and
        $defaults.chunk_size_bytes -eq 33554432 -and
        $defaults.max_active_chunks_per_node -eq 2 -and $defaults.max_active_chunks_global -eq 4
    Add-BaselineCheck '设计参数 JSON' $defaultsValid '32 MiB；每节点 2；全局 4；明确标记为设计参数'

    $remoteLicense = ((& git show origin/main:LICENSE) -join "`n").TrimEnd()
    $localLicense = ([IO.File]::ReadAllText((Join-Path $baselineRoot 'LICENSE')) -replace "`r`n", "`n").TrimEnd()
    Add-BaselineCheck '远端许可证保留' ($localLicense -eq $remoteLicense) '与 origin/main:LICENSE 对比'
    $missingAssets = @('assets/branding/hopsend.ico','assets/branding/hopsend-icon.svg',
        'assets/diagrams/system-architecture.svg','assets/diagrams/system-architecture.png') |
        Where-Object { -not (Test-Path -LiteralPath $_ -PathType Leaf) }
    Add-BaselineCheck '品牌与结构图' (@($missingAssets).Count -eq 0) (@($missingAssets) -join ', ')

    $ignoredPaths = @('dist/probe.exe','work/probe.txt','runtime/state.db',
        'apps/desktop/frontend/node_modules/probe.js','apps/desktop/frontend/dist/index.html',
        'apps/desktop/frontend/wailsjs/probe.js','machine.local.yaml','probe.key')
    $ignoredActual = @(& git check-ignore --no-index -- @ignoredPaths)
    $notIgnored = @($ignoredPaths | Where-Object { $_ -notin $ignoredActual })
    Add-BaselineCheck '运行数据与产物忽略' ($notIgnored.Count -eq 0) ($notIgnored -join ', ')

    $gitExecutable = (Get-Command git -CommandType Application | Select-Object -First 1).Source
    $gitRoot = Split-Path (Split-Path $gitExecutable -Parent) -Parent
    $gitShell = Join-Path $gitRoot 'bin/sh.exe'
    if (-not (Test-Path -LiteralPath $gitShell)) { throw '未找到 Git for Windows 的 bin/sh.exe' }
    $hookRoot = Join-Path $baselineRoot '.githooks'
    $hookFiles = @('pre-commit','commit-msg','pre-push')
    $crlfHooks = @($hookFiles | Where-Object { [IO.File]::ReadAllText((Join-Path $hookRoot $_)).Contains("`r") })
    Add-BaselineCheck '钩子 LF 换行' ($crlfHooks.Count -eq 0) ($crlfHooks -join ', ')

    $probeRoot = Join-Path $baselineRoot ('work/s0-hook-probe-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $probeRoot -Force | Out-Null
    & git -C $probeRoot init --quiet --initial-branch=main
    if ($LASTEXITCODE -ne 0) { throw '钩子隔离仓库初始化失败' }
    Push-Location $probeRoot
    try {
        $null = & $gitShell (Join-Path $hookRoot 'pre-commit') 2>&1
        $mainBlocked = $LASTEXITCODE -ne 0
        & git symbolic-ref HEAD refs/heads/test/s0-hook-probe
        $null = & $gitShell (Join-Path $hookRoot 'pre-commit') 2>&1
        $testAllowed = $LASTEXITCODE -eq 0
    } finally { Pop-Location }
    Add-BaselineCheck '提交分支限制' ($mainBlocked -and $testAllowed) '隔离仓库：main 被拒绝；test 分支被允许'

    $utf8 = [Text.UTF8Encoding]::new($false)
    $messageFile = Join-Path $probeRoot 'commit-message.txt'
    [IO.File]::WriteAllText($messageFile, "功能(P1.2): 完成分块恢复验证`n", $utf8)
    $null = & $gitShell (Join-Path $hookRoot 'commit-msg') $messageFile 2>&1
    $validAllowed = $LASTEXITCODE -eq 0
    [IO.File]::WriteAllText($messageFile, "feat: incomplete stage`n", $utf8)
    $null = & $gitShell (Join-Path $hookRoot 'commit-msg') $messageFile 2>&1
    $invalidBlocked = $LASTEXITCODE -ne 0
    [IO.File]::WriteAllText($messageFile, "功能(P9.1): 无效阶段`n", $utf8)
    $null = & $gitShell (Join-Path $hookRoot 'commit-msg') $messageFile 2>&1
    $invalidStageBlocked = $LASTEXITCODE -ne 0
    Add-BaselineCheck '中文标题格式' ($validAllowed -and $invalidBlocked -and $invalidStageBlocked) '规范标题被允许；错误格式与阶段被拒绝'

    $zeroSha = '0000000000000000000000000000000000000000'
    $null = "refs/heads/test/probe $zeroSha refs/heads/main $zeroSha" |
        & $gitShell (Join-Path $hookRoot 'pre-push') origin $remote 2>&1
    $mainPushBlocked = $LASTEXITCODE -ne 0
    $null = "refs/heads/test/probe $zeroSha refs/heads/test/probe $zeroSha" |
        & $gitShell (Join-Path $hookRoot 'pre-push') origin $remote 2>&1
    $testPushAllowed = $LASTEXITCODE -eq 0
    Add-BaselineCheck '主分支推送限制' ($mainPushBlocked -and $testPushAllowed) '模拟协议输入；未向远端 main 发起测试推送'

    $diffOutput = (& git diff --check 2>&1) -join "`n"
    $diffOk = $LASTEXITCODE -eq 0
    $stagedOutput = (& git diff --cached --check 2>&1) -join "`n"
    $stagedOk = $LASTEXITCODE -eq 0
    Add-BaselineCheck 'Git 差异格式' ($diffOk -and $stagedOk) ($diffOutput + $stagedOutput)

    $report = [pscustomobject]@{
        stage = 'S0'
        branch = $branch
        base_commit = (& git rev-parse origin/main).Trim()
        all_passed = @($baselineChecks | Where-Object { -not $_.passed }).Count -eq 0
        markdown_files_checked = $markdownFiles.Count
        checks = $baselineChecks.ToArray()
        business_tests_run = $false
    }
    $json = $report | ConvertTo-Json -Depth 6
    if ($ReportPath) {
        $absoluteReport = [IO.Path]::GetFullPath((Join-Path $baselineRoot $ReportPath))
        [IO.Directory]::CreateDirectory((Split-Path $absoluteReport -Parent)) | Out-Null
        [IO.File]::WriteAllText($absoluteReport, $json + "`n", [Text.UTF8Encoding]::new($false))
    }
    Write-Output $json
    if (-not $report.all_passed) { exit 1 }
} finally {
    Pop-Location
}
