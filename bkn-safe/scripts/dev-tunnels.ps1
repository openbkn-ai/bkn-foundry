# Start SSH local forwards to remote cluster services on 118.196.139.75.
# Prerequisite: on the remote host, kubectl port-forwards are already listening on
# 127.0.0.1:3307 (mariadb), 4444 (hydra public), 4445 (hydra admin).
#
# Usage (PowerShell):
#   .\scripts\dev-tunnels.ps1
# Keep this window open while developing.

param(
    [string]$RemoteHost = "118.196.139.75",
    [string]$RemoteUser = "root",
    [int]$MariaDBLocal = 3307,
    [int]$HydraPublicLocal = 4444,
    [int]$HydraAdminLocal = 4445
)

$ErrorActionPreference = "Stop"

function Test-LocalPort([int]$Port) {
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $iar = $client.BeginConnect("127.0.0.1", $Port, $null, $null)
        $ok = $iar.AsyncWaitHandle.WaitOne(1500, $false)
        $open = $ok -and $client.Connected
        $client.Close()
        return $open
    } catch {
        return $false
    }
}

foreach ($port in @($MariaDBLocal, $HydraPublicLocal, $HydraAdminLocal)) {
    if (Test-LocalPort $port) {
        Write-Host "Port $port already open on 127.0.0.1 — skip starting duplicate tunnel for this port."
    }
}

Write-Host "Opening SSH tunnels to ${RemoteUser}@${RemoteHost} ..."
Write-Host "  localhost:$MariaDBLocal      -> remote MariaDB"
Write-Host "  localhost:$HydraPublicLocal  -> remote Hydra public"
Write-Host "  localhost:$HydraAdminLocal   -> remote Hydra admin (required for admin API token introspect)"
Write-Host ""
Write-Host "Press Ctrl+C to stop."

ssh -N `
    -o ServerAliveInterval=30 `
    -o ExitOnForwardFailure=yes `
    -L "${MariaDBLocal}:127.0.0.1:${MariaDBLocal}" `
    -L "${HydraPublicLocal}:127.0.0.1:${HydraPublicLocal}" `
    -L "${HydraAdminLocal}:127.0.0.1:${HydraAdminLocal}" `
    "${RemoteUser}@${RemoteHost}"
