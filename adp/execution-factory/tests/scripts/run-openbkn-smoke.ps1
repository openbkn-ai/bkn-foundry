param([string]$Token = $env:OPENBKN_TOKEN)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if (-not $Token) {
  Write-Error "Set OPENBKN_TOKEN or pass -Token."
}

$envIni = Join-Path $root "config\env.ini"
$envExample = Join-Path $root "config\env.openbkn.example.ini"
if (-not (Test-Path $envIni)) {
  Copy-Item $envExample $envIni
  Write-Host "Created config/env.ini from env.openbkn.example.ini"
}

$env:OPENBKN_TOKEN = $Token
Set-Location $root
py -m pytest testcases/openbkn-smoke --confcutdir=testcases/openbkn-smoke -q
