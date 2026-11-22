# Dot-source this file to prefer the verified, project-local tools.
$hopsendRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$hopsendGo = Join-Path $hopsendRoot 'work/.tools/go/bin'
$hopsendBin = Join-Path $hopsendRoot 'work/.tools/bin'
if (Test-Path -LiteralPath (Join-Path $hopsendGo 'go.exe')) { $env:PATH = $hopsendGo + ';' + $env:PATH }
if (Test-Path -LiteralPath $hopsendBin) { $env:PATH = $hopsendBin + ';' + $env:PATH }
$env:GOTOOLCHAIN = 'local'
