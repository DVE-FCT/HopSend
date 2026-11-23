param(
    [Parameter(Mandatory)][string]$Path,
    [ValidateRange(1,4096)][int]$SizeMiB = 256,
    [ValidateRange(0,1048575)][int]$TailBytes = 17,
    [int]$Seed = 20261123
)
$ErrorActionPreference = 'Stop'
$fixturePath = [IO.Path]::GetFullPath($Path)
if (Test-Path -LiteralPath $fixturePath) { throw '拒绝覆盖已有文件' }
$fixtureRandom = [Random]::new($Seed)
$fixtureBuffer = [byte[]]::new(1MB)
$fixtureStream = [IO.File]::Open($fixturePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try {
    for ($fixtureIndex = 0; $fixtureIndex -lt $SizeMiB; $fixtureIndex++) {
        $fixtureRandom.NextBytes($fixtureBuffer)
        $fixtureStream.Write($fixtureBuffer, 0, $fixtureBuffer.Length)
    }
    if ($TailBytes -gt 0) {
        $fixtureRandom.NextBytes($fixtureBuffer)
        $fixtureStream.Write($fixtureBuffer, 0, $TailBytes)
    }
    $fixtureStream.Flush($true)
} finally { $fixtureStream.Dispose() }
[pscustomobject]@{
    generator = 'System.Random(seed).NextBytes; 1MiB buffer per block; optional tail; record runtime for repeatability'
    seed = $Seed
    bytes = (Get-Item -LiteralPath $fixturePath).Length
    sha256 = (Get-FileHash -LiteralPath $fixturePath -Algorithm SHA256).Hash.ToLowerInvariant()
    dotnet = [Environment]::Version.ToString()
} | ConvertTo-Json
