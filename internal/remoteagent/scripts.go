package remoteagent

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"unicode/utf16"
)

func powershell(script string, value any) string {
	data, _ := json.Marshal(value)
	source := "$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);$p=([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString(data) + "'))|ConvertFrom-Json);" + script
	chars := utf16.Encode([]rune(source))
	encoded := make([]byte, len(chars)*2)
	for i, c := range chars {
		binary.LittleEndian.PutUint16(encoded[i*2:], c)
	}
	return "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(encoded)
}

const environmentScript = `
if($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitOperatingSystem){throw '需要 Windows x64 节点'}
if($p.config -notmatch '^[A-Za-z]:[\\/]'){throw '远端 Agent 配置需要绝对路径'}
$config=[IO.Path]::GetFullPath($p.config)
if(-not(Test-Path -LiteralPath $config -PathType Leaf)){throw '远端配对配置不存在，请先完成一次设备初始化与授权'}
$root=Join-Path $env:LOCALAPPDATA 'HopSend/Agent/managed'
[IO.Directory]::CreateDirectory($root)|Out-Null
@{root=$root.Replace('\','/');config=$config;platform='windows-x64'}|ConvertTo-Json -Compress
`

const verifyScript = `
$file=[IO.File]::OpenRead($p.executable);$hasher=[Security.Cryptography.SHA256]::Create();try{$hash=([BitConverter]::ToString($hasher.ComputeHash($file))).Replace('-','').ToLowerInvariant()}finally{$file.Dispose();$hasher.Dispose()};if($hash -ne $p.sha256){throw '远端 Agent 程序 SHA-256 不一致'}
@{verified=$true}|ConvertTo-Json -Compress
`

const runScript = `
$file=[IO.File]::OpenRead($p.executable);$hasher=[Security.Cryptography.SHA256]::Create();try{$hash=([BitConverter]::ToString($hasher.ComputeHash($file))).Replace('-','').ToLowerInvariant()}finally{$file.Dispose();$hasher.Dispose()};if($hash -ne $p.sha256){throw '远端 Agent 程序 SHA-256 不一致'}
& $p.executable --config $p.config
exit $LASTEXITCODE
`

// File.Move without overwrite publishes only a fully verified temporary file.
// Interrupted deployment can leave its random temporary file, never a corrupt
// executable under the content hash expected by the next connection.
const publishScript = `
$file=[IO.File]::OpenRead($p.temporary);$hasher=[Security.Cryptography.SHA256]::Create();try{$hash=([BitConverter]::ToString($hasher.ComputeHash($file))).Replace('-','').ToLowerInvariant()}finally{$file.Dispose();$hasher.Dispose()};if($hash -ne $p.sha256){throw 'Agent 暂存程序 SHA-256 不一致'}
if(-not(Test-Path -LiteralPath $p.executable)){[IO.File]::Move($p.temporary,$p.executable)}
@{published=$true}|ConvertTo-Json -Compress
`
