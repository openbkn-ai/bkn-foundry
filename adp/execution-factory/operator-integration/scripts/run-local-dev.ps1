param(
  [string]$DbHost = "127.0.0.1",
  [int]$DbPort = 3306,
  [string]$DbUser = "root",
  [string]$DbPassword = $env:OPENBKN_DB_PASSWORD,
  [string]$DbName = "dip_data_operator_hub"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$configDir = Join-Path $root "server\infra\config"
$secretLocal = Join-Path $configDir "agent-operator-integration-secret.local.yaml"
$secretExample = Join-Path $configDir "agent-operator-integration-secret.local.example.yaml"

if (-not $DbPassword) {
  Write-Error "Set OPENBKN_DB_PASSWORD or pass -DbPassword."
}

if (-not (Test-Path $secretLocal)) {
  if (-not (Test-Path $secretExample)) {
    Write-Error "Missing secret example: $secretExample"
  }
  Copy-Item $secretExample $secretLocal
  (Get-Content $secretLocal -Raw) `
    -replace 'host: "127.0.0.1"', "host: `"$DbHost`"" `
    -replace 'port: 3306', "port: $DbPort" `
    -replace 'user_name: "root"', "user_name: `"$DbUser`"" `
    -replace 'password: "<REPLACE_ME>"', "password: `"$DbPassword`"" `
    -replace 'db_name: "dip_data_operator_hub"', "db_name: `"$DbName`"" |
    Set-Content $secretLocal -NoNewline
  Write-Host "Created $secretLocal"
}

$env:CONFIG_PROFILE = $configDir
$env:AUTH_ENABLED = "false"
$env:BUSINESS_DOMAIN_ENABLED = "false"

# Use local secret override (copy real values into secret.local.yaml)
Copy-Item $secretLocal (Join-Path $configDir "agent-operator-integration-secret.yaml") -Force

Write-Host "Starting agent-operator-integration on :9000 (AUTH_ENABLED=false) ..."
Set-Location $root
go run ./server/main.go
