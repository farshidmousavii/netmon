# export-dhcp-leases.ps1 — DHCP lease export for the Bidar inventory daemon
#
# WHAT THIS IS
#   A scheduled-task script that runs ON the DHCP server itself (typically
#   a Domain Controller) and dumps the current DHCPv4 leases to a JSON
#   file. The Bidar daemon's DHCP collector (source_type = 'windows') reads
#   that file from a filesystem path you make reachable — e.g. an OS-level
#   SMB mount — and ingests the leases. Bidar does NOT connect to this
#   server, does not deploy or run this script, and needs no credential
#   here at all.
#
# WHY THIS EXISTS (no special account needed)
#   Reading DHCP leases live requires either WinRM plus the 'DHCP Users'
#   group (which since Windows Server 2012 must be provisioned once with
#   Add-DhcpServerSecurityGroup — commonly never done, especially on
#   Domain Controllers), or an admin account. This file-export method
#   sidesteps all of that: it runs as whatever account the local Scheduled
#   Task uses (typically SYSTEM, which already has full local DHCP access
#   on the box it runs on) and only ever writes a file.
#
# SETUP (one-time, per DHCP server)
#   1. Pick an output path reachable by the Bidar host, e.g. a directory
#      on an SMB share mounted on both machines.
#   2. Create a Scheduled Task running this script on your preferred
#      cadence (e.g. hourly), as SYSTEM:
#        schtasks /create /tn "Bidar DHCP Lease Export" /sc hourly \
#          /ru SYSTEM /tr "powershell -ExecutionPolicy Bypass -File C:\path\to\export-dhcp-leases.ps1 -OutputPath D:\shares\dhcp\leases-center.json"
#   3. In the database, set the matching dhcp_sources row's
#      connection_config to {"path": "<the same output path as seen from
#      the Bidar host>"} (and source_type = 'windows').
#   4. Bidar's collector checks the file's exported_at timestamp and
#      refuses data older than BIDAR_DHCP_STALENESS (default 24h), so a
#      broken scheduled task is visible as a failed source, never silently
#      treated as fresh.
#
# REQUIREMENTS
#   - DHCP Server administration module (Get-DhcpServerv4Lease ships with
#     the DHCP Server role; the cmdlet is available on the server itself).
#   - PowerShell 5.1+ (Get-DhcpServerv4Lease -ComputerName requires
#     Windows Server 2012+ DHCP).
#
# This script intentionally does NOT swallow errors: if the DHCP query or
# the file write fails, the Scheduled Task fails and the next run's
# absence of a fresh timestamp tells the collector the source is stale.
# No secrets are involved; the file contains only lease data.

param(
    [Parameter(Mandatory = $true)]
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'

$leases = @(Get-DhcpServerv4Lease -ComputerName $env:COMPUTERNAME -AllLeases)

$export = [pscustomobject]@{
    exported_at = (Get-Date).ToUniversalTime().ToString('o')  # RFC3339/ISO-8601; staleness is checked against this
    server      = $env:COMPUTERNAME
    lease_count = $leases.Count
    leases      = $leases
}

$json = $export | ConvertTo-Json -Depth 5

# Directory must exist; write via a temp file + rename so the collector
# never reads a half-written export.
$dir = Split-Path -Parent $OutputPath
if (-not (Test-Path $dir)) {
    throw "Output directory does not exist: $dir"
}
$tmp = "$OutputPath.tmp"
$json | Out-File -FilePath $tmp -Encoding utf8
Move-Item -Force -Path $tmp -Destination $OutputPath
