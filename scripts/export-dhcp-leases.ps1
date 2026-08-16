# export-dhcp-leases.ps1 — dumps this server's DHCPv4 leases to JSON for the Bidar daemon.
#
# Run it on the DHCP server itself. No parameters, no scope IDs, no special
# account — any scheduled task (or double-click) works. Scopes are found
# automatically (Get-DhcpServerv4Scope); every scope's leases are exported.
#
# Writes: C:\ProgramData\Bidar\dhcp-leases-<COMPUTERNAME>.json
# (the computer name in the filename keeps multiple servers' exports from
# colliding when their folders are all mounted into the daemon)
#
# Optional: -OutputPath <path> to write somewhere else, e.g. a share:
#   powershell -File export-dhcp-leases.ps1 -OutputPath '\\nas\dhcp\leases.json'

param(
    [string]$OutputPath = ""
)

$ErrorActionPreference = 'Stop'

if (-not $OutputPath) {
    $OutputPath = Join-Path 'C:\ProgramData\Bidar' "dhcp-leases-${env:COMPUTERNAME}.json"
}

# Enumerate scopes automatically — the user never supplies a ScopeId.
# (Per-scope queries with -ScopeId work on every DhcpServer module version,
# including older ones where bare -AllLeases would prompt for ScopeId.)
$scopes = @(Get-DhcpServerv4Scope)
if ($scopes.Count -eq 0) {
    throw 'No DHCP scopes found on this server.'
}

$leases = @()
foreach ($scope in $scopes) {
    # Without -ClientId/-LeaseId, this returns every lease in the scope.
    $leases += @(Get-DhcpServerv4Lease -ScopeId $scope.ScopeId)
}

$export = [pscustomobject]@{
    exported_at = (Get-Date).ToUniversalTime().ToString('o')  # RFC3339; the daemon checks this timestamp
    server      = $env:COMPUTERNAME
    lease_count = $leases.Count
    leases      = $leases
}

$dir = Split-Path -Parent $OutputPath
New-Item -ItemType Directory -Force -Path $dir | Out-Null

$tmp = "$OutputPath.tmp"
$export | ConvertTo-Json -Depth 5 | Out-File -FilePath $tmp -Encoding utf8
Move-Item -Force -Path $tmp -Destination $OutputPath
