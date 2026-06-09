param(
  [string]$Source = "$env:KEWEAVER_ROOT\adp\execution-factory\tests",
  [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$destRoot = Split-Path -Parent $PSScriptRoot

if (-not $Source) {
  $Source = "e:\00_code_workspace\keweaver\adp\execution-factory\tests"
}

if (-not (Test-Path $Source)) {
  Write-Error "Agent AT source not found: $Source. Set KEWEAVER_ROOT or pass -Source."
}

$folders = @(
  "testcases\data-operator-hub",
  "data",
  "resource",
  "response",
  "api-docs",
  "helm"
)

Write-Host "Syncing full Agent AT payloads from:"
Write-Host "  Source: $Source"
Write-Host "  Dest:   $destRoot"

foreach ($folder in $folders) {
  $src = Join-Path $Source $folder
  $dst = Join-Path $destRoot $folder
  if (-not (Test-Path $src)) {
    Write-Warning "Skip missing source folder: $folder"
    continue
  }
  if ($DryRun) {
    Write-Host "[dry-run] would copy $src -> $dst"
    continue
  }
  if (Test-Path $dst) {
    Remove-Item $dst -Recurse -Force
  }
  Copy-Item $src $dst -Recurse -Force
  Write-Host "Copied $folder"
}

if (-not $DryRun) {
  $envExample = Join-Path $Source "config\env.ini.example"
  if (Test-Path $envExample) {
    Copy-Item $envExample (Join-Path $destRoot "config\env.ini.example") -Force
  }
  Write-Host "Done. Full suite: py -m pytest testcases/data-operator-hub -q"
}
