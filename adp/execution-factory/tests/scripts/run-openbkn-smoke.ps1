param(
  [string]$Token = $env:OPENBKN_TOKEN,
  [string]$BusinessDomain = $env:OPENBKN_BUSINESS_DOMAIN,
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

if (-not $BusinessDomain) {
  $BusinessDomain = "bd_public"
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
$env:OPENBKN_BUSINESS_DOMAIN = $BusinessDomain

Set-Location $root
py -m pytest testcases/openbkn-smoke --confcutdir=testcases/openbkn-smoke -q
