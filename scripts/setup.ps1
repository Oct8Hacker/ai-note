# setup.ps1 - Windows version of setup.sh (FR-SETUP-2). Creates .env from
# .env.example and fills only the values that are empty; safe to run again.
#
#   powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
#   $env:ENV_FILE = "path"; scripts\setup.ps1      # custom target
# Works in Windows PowerShell 5.1 and PowerShell 7.
$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
$EnvFile = if ($env:ENV_FILE) { $env:ENV_FILE } else { Join-Path $Root '.env' }
$Example = Join-Path $Root '.env.example'
$Utf8NoBom = New-Object System.Text.UTF8Encoding $false

# .env is written with LF line endings and no BOM, as docker compose expects.
function Save-Lines([string[]]$Lines) {
    [System.IO.File]::WriteAllText($EnvFile, (($Lines -join "`n") + "`n"), $Utf8NoBom)
}

if (-not (Test-Path $EnvFile)) {
    Save-Lines (Get-Content $Example)
    Write-Host "created $EnvFile from .env.example"
}

function Get-Value([string]$Key) {
    $line = Get-Content $EnvFile | Where-Object { $_ -like "$Key=*" } | Select-Object -Last 1
    if ($null -eq $line) { return '' }
    return $line.Substring($Key.Length + 1)
}

function Set-IfEmpty([string]$Key, [string]$Value) {
    if (Get-Value $Key) { return }
    $lines = @(Get-Content $EnvFile)
    $found = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -eq "$Key=") { $lines[$i] = "$Key=$Value"; $found = $true }
    }
    if (-not $found) { $lines += "$Key=$Value" }
    Save-Lines $lines
    Write-Host "set $Key"
}

function New-RandomBytes([int]$Count) {
    $bytes = New-Object byte[] $Count
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return ,$bytes
}

Set-IfEmpty 'TOKEN_ENC_KEY' ([Convert]::ToBase64String((New-RandomBytes 32)))
Set-IfEmpty 'ADMIN_TOKEN' (((New-RandomBytes 24) | ForEach-Object { $_.ToString('x2') }) -join '')

$provider = Get-Value 'LLM_PROVIDER'
if (-not $provider) { $provider = 'gemini' }
$keyVar = switch ($provider) {
    'gemini' { 'GEMINI_API_KEY' }
    'groq' { 'GROQ_API_KEY' }
    'openrouter' { 'OPENROUTER_API_KEY' }
    default { '' }
}
if ($keyVar -and -not (Get-Value $keyVar)) {
    if ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
        $secure = Read-Host "$keyVar (input hidden, Enter to skip)" -AsSecureString
        $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
        try { $apiKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
        finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
        if ($apiKey) { Set-IfEmpty $keyVar $apiKey }
    }
    if (-not (Get-Value $keyVar)) { Write-Host "note: $keyVar is empty; add it to $EnvFile before starting the stack" }
}

Write-Host @"

Next steps:
  1. docker compose -f deploy/docker-compose.yml --env-file .env up -d
  2. curl http://localhost:8080/healthz          (wait for "status": "ok")
  3. scripts/doctor.sh  (Git Bash or WSL)        (checks the LLM key, yt-dlp, ffmpeg, YouTube)
  4. Load the extension and connect Notion       (README -> Quick start)
"@
