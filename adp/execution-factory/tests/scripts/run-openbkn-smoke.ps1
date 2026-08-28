param(
  [string]$Token = $env:OPENBKN_TOKEN,
  [switch]$AuthDisabled
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if ($AuthDisabled -or $env:OPENBKN_AUTH_ENABLED -eq "false") {
  $AuthDisabled = $true
}

if (-not $AuthDisabled -and -not $Token) {
  Write-Error "Set OPENBKN_TOKEN, pass -Token, or use -AuthDisabled for local dev."
}

$envIni = Join-Path $root "config\env.ini"
$envExample = Join-Path $root "config\env.openbkn.example.ini"
if (-not (Test-Path $envIni)) {
  Copy-Item $envExample $envIni
  Write-Host "Created config/env.ini from env.openbkn.example.ini"
}

if ($AuthDisabled) {
  $env:OPENBKN_AUTH_ENABLED = "false"
} else {
  $env:OPENBKN_TOKEN = $Token
}
Set-Location $root
py -m pytest testcases/openbkn-smoke --confcutdir=testcases/openbkn-smoke -q
