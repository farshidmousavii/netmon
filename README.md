# Bidar

Bidar is a powerful CLI tool for network device monitoring, configuration backup, and bulk command execution across Cisco and MikroTik devices.

---

# Features

- Health Monitoring — Ping + SNMP queries (vendor, hostname, uptime)
- Configuration Backup — Automated backup with archiving support
- Bulk Command Execution — Run commands across multiple devices
- Config Comparison — Line-by-line diff between backup files
- Flexible Configuration — YAML or CSV format with auto-detection
- Concurrent Operations — Fast parallel execution across devices
- Multiple Output Formats — Human-readable or JSON output

## Supported Devices

- Cisco — IOS, IOS-XE, NX-OS
- MikroTik — RouterOS

---

## Installation

Build from Source

```bash
git clone https://github.com/farshidmousavii/bidar.git
cd bidar
go build -o bidar ./cmd/bidar
```

Run Directly

```bash
go run ./cmd/bidar [command]
```

## Quick Start (inventory daemon)

The daemon (AD + ARP + DHCP + ICMP collectors) runs in Docker with Postgres.
The only files you touch: your device config (`config.yaml` or
`devices.csv`), `.env`, and the `bidar` CLI. No raw database access needed
for onboarding.

1. **Prepare environment**

```bash
cp .env.example .env
# edit .env: set a real BIDAR_MASTER_KEY (openssl rand -base64 32)
# and a real POSTGRES_PASSWORD. Optionally BIDAR_AD_* to enable the AD
# provider, and the BIDAR_*_INTERVAL cadences.
```

2. **Start the database and apply migrations**

```bash
docker compose up -d postgres
docker compose run --rm migrate      # applies schema; safe to re-run
```

3. **Import your existing device config** (one-time; file -> database,
   never writes back)

```bash
docker compose run --rm bidar import-devices --config /config.yaml   # run inside the container
# or from the host, with BIDAR_DATABASE_URL pointing at the stack:
BIDAR_DATABASE_URL=postgres://bidar:PASSWORD@localhost:5432/bidar BIDAR_MASTER_KEY=... \
  ./bidar import-devices --config config.yaml
```

4. **Mark the core/L3 switches** (the ARP collector polls only these)

```bash
docker compose exec bidar /usr/local/bin/bidar devices list                       # see what was imported
docker compose exec bidar /usr/local/bin/bidar devices list --role=unassigned
docker compose exec bidar /usr/local/bin/bidar devices set-role <name-or-ip> core # one per core switch
```

5. **Register DHCP sources and point them at their lease-export files**
   (files produced by `scripts/export-dhcp-leases.ps1` on each Windows
   DHCP server). The daemon supports any number of sources, mixed types:

```bash
docker compose exec bidar /usr/local/bin/bidar dhcp-sources list
# add a Windows source (path set here or later via set-path):
docker compose exec bidar /usr/local/bin/bidar dhcp-sources add center-dhcp windows --path /mnt/dhcp/leases-center.json
# add a MikroTik source (password encrypted at rest):
docker compose exec bidar /usr/local/bin/bidar dhcp-sources add ros-dhcp mikrotik \
  --host 192.0.2.12 --username admin --password 'CHANGE_ME'
docker compose exec bidar /usr/local/bin/bidar dhcp-sources set-path center-dhcp /mnt/dhcp/leases-center.json
```

   **Both path forms work in `set-path`.** The daemon reads the path as
   seen inside its Linux container, but you don't have to translate by
   hand: set `DHCP_SHARE_SRC` in `.env` to the share you actually see
   (on Windows/Docker Desktop: `//dc01/dhcp$` or a mapped drive letter
   like `Z:/dhcp`; on Linux: any host path), and compose mounts it
   read-only at `/mnt/dhcp`. Then either form is accepted:

```bash
# the Windows path you see on your machine — translated automatically:
docker compose exec bidar /usr/local/bin/bidar dhcp-sources set-path center-dhcp '\\dc01\dhcp$\leases.json'
# or the container-internal path directly:
docker compose exec bidar /usr/local/bin/bidar dhcp-sources set-path center-dhcp /mnt/dhcp/leases.json
```

   A Windows-style path that doesn't match the configured share is
   stored but warned about — the daemon can only read mounted paths.
   (If your DHCP servers are plain member servers, the WinRM method in
   `docs/architecture.md` is the future alternative — Phase 1 uses the
   file export only.)

6. **Start the daemon**

```bash
docker compose up -d
docker compose logs -f bidar          # provider runs land in provider_runs
docker compose exec bidar /usr/local/bin/bidar hosts   # the live inventory
```

Role and path changes take effect on the next scheduled poll cycle — no
daemon restart needed.

### Legacy CLI (monitor / backup / exec / diff / init / tui-config)

The original SSH-based CLI still works exactly as before, reading
`config.yaml`/`devices.csv` directly. See the sections below.

### Advanced: direct database access (debugging only)

Routine setup goes through the `bidar` commands above. If you need to
inspect or fix something by hand:

```bash
docker compose exec -T postgres psql -U bidar -d bidar
```

Use this for debugging, not as the normal path — if a routine operation
requires SQL, that is a missing `bidar` subcommand, not a workflow.

# Commands
# Commands

## monitor

Health check devices with ping and SNMP.

```bash
# Basic monitoring
./bidar monitor

# Skip backup during monitoring
./bidar monitor --skip-backup

# Skip SNMP queries
./bidar monitor --skip-snmp

# JSON output
./bidar monitor --json

# Enable file logging
./bidar monitor --log

# Override SNMP settings
./bidar monitor --snmp-community private --snmp-timeout 20

# Override backup directory
./bidar monitor --backup-dir /opt/backups


Options:

-l, --log — Enable file logging
--skip-backup — Skip configuration backup
--skip-snmp — Skip SNMP queries
-j, --json — Output as JSON
--snmp-community <string> — Override SNMP community
--snmp-timeout <int> — Override SNMP timeout (seconds)
--backup-dir <path> — Override backup directory
--backup-archive <path> — Override archive path
```

# backup

Backup device configurations.

```bash
# Basic backup
./bidar backup

# With logging
./bidar backup --log

# JSON output
./bidar backup --json
```

**Options:**

- `-l, --log` — Enable file logging
- `-j, --json` — Output as JSON

**Backup File Naming:**

```
backups/
├── cisco/
│   └── 2026-04-20_11-30/
│       ├── Core-Switch.txt
│       └── Dist-Switch-01.txt
└── mikrotik/
    └── 2026-04-20_11-30/
        └── Edge-Router.txt
```

# exec

Execute commands on devices.

```bash
# Single device - show command
./bidar exec -d core-switch -c "show ip interface brief"
#or
./bidar exec -d 192.168.1.1 -c "show ip interface brief"

# All Cisco devices - config command
./bidar exec --type cisco -c "interface gi0/1" -c "description UPLINK"

# With config save (Cisco only)
./bidar exec -d core-switch -c "interface gi0/1" -c "shutdown" --save

# Dry run (preview without execution)
./bidar exec --type cisco -c "interface gi0/1" -c "shutdown" --dry-run

# Save output to file
./bidar exec --type cisco -c "show running-config" -o output.txt

# Interactive mode
./bidar exec --type cisco
# Enter commands one per line, empty line to finish
Target Selection (choose one):

-d, --device <name|ip> — Execute on specific device
--type <vendor> — Execute on all devices of type (cisco/mikrotik)
```

## Command Options:

```
-c, --command <cmd> — Command to execute (repeatable for multiple commands)
--save — Save config after execution (Cisco: write memory)
--dry-run — Preview commands without execution
-o, --output <file> — Save output to file (.txt or .log)
```

## Command Auto-Detection:

Commands starting with show, display, ping, traceroute → Exec mode

All other commands → Config mode (automatic conf t → commands → end)

## Examples:

```bash
# Show command (auto-detected)
./bidar exec -d core-switch -c "show ip route"

# Config commands (auto-detected, enters config mode)
./bidar exec --type cisco \
  -c "interface gi0/1" \
  -c "description UPLINK_TO_CORE" \
  -c "no shutdown" \
  --save

# Dry run to preview
./bidar exec --type cisco \
  -c "interface gi0/1" \
  -c "shutdown" \
  --dry-run

# Execute and save output
./bidar exec --type cisco -c "show run" -o configs.txt
```

# diff

Compare two backup files.

```bash
./bidar diff backups/cisco/2026-04-20_10-00/Core-Switch.txt \
                   backups/cisco/2026-04-20_11-00/Core-Switch.txt
```

**Output:**

```
Found 3 differences:

Line 42:
  - interface GigabitEthernet0/1
  + interface GigabitEthernet0/2

Line 58:
  - description OLD_LINK
  + description NEW_UPLINK

Line 120:
  -  shutdown
```

# init

Initialize configuration files.

```bash
# Create YAML config (default)
./bidar init

# Create CSV config
./bidar init --format csv
Options:

--format <yaml|csv> — Config format (default: yaml)
```

## Global Flags

These flags apply to all commands:

```
--config <file> — Path to config file (default: config.yaml)

Auto-detection:

.yaml, .yml → YAML format
.csv → CSV format
```

## Examples:

```bash
./bidar monitor --config devices.csv
./bidar backup --config /etc/bidar/config.yaml
./bidar exec --config production.csv --type cisco -c "show version"
```

## Configuration

YAML Format
Best for:

- Shared credentials across devices
- Complex SNMP/backup settings
- Template-based device groups

## Structure:

```yaml
version: 1

credentials:
  <credential-name>:
    username: <username>
    password: <password>

devices:
  - name: <device-name>
    ip: <ip-address>
    port: <ssh-port>
    vendor: <cisco|mikrotik>
    credential: <credential-name>

snmp:
  community: <community-string>
  timeout: <timeout-seconds>

backup:
  directory: <backup-path>
  archive_path: <archive-path>
```

## CSV Format

Best for:

- Bulk device import
- Per-device credentials
- Quick setup from spreadsheets

## Structure:

```csv
#snmp_community=<value>
#snmp_timeout=<seconds>
#backup_dir=<path>
#backup_archive=<path>
name,ip,port,vendor,username,password
<name>,<ip>,<port>,<vendor>,<user>,<pass>
Notes:

Lines starting with #key=value define global settings
If settings are omitted, defaults are used
Each device has its own username/password

Defaults:

SNMP community: public
SNMP timeout: 10 seconds
Backup directory: backups
Archive path: empty (no archiving)
```

# Examples

## Monitoring

```bash
# Basic monitoring (YAML config)
./bidar monitor

# Monitor with CSV, override SNMP settings
./bidar monitor --config devices.csv \
  --snmp-community private \
  --snmp-timeout 20

# Monitor without SNMP
./bidar monitor --skip-snmp

# Monitor with logging and JSON output
./bidar monitor --log --json > report.json
```

## Backup

```bash
# Backup all devices
./bidar backup

# Backup with custom directory
./bidar monitor --backup-dir /mnt/backups

# Backup with archiving
./bidar monitor --backup-archive /mnt/archive
```

## Bulk Execution

```bash
# Check version on all Cisco devices
./bidar exec --type cisco -c "show version"

# Configure interface on specific device
./bidar exec -d core-switch \
  -c "interface gi0/1" \
  -c "description UPLINK_TO_DATACENTER" \
  -c "no shutdown" \
  --save

# Dry run before execution
./bidar exec --type cisco \
  -c "no ip http server" \
  -c "no ip http secure-server" \
  --dry-run

# Interactive mode
./bidar exec --type cisco
Enter commands (one per line, empty line to finish):
interface gi0/1
description MGMT_INTERFACE
no shutdown
[Enter]

⚠ You are about to execute on 5 devices:
  • core-switch (192.168.1.1)
  • dist-switch-01 (192.168.2.1)
  ...
Continue? (yes/no): yes
```

# Comparison

Compare two backup files

```bash
./bidar diff \
  backups/cisco/2026-04-19_23-00/Core-Switch.txt \
  backups/cisco/2026-04-20_11-00/Core-Switch.txt
```

---

## Output Examples

### Monitor Output

```
══════════════════════════════════════════════════════════════════════
           NETWORK DEVICE HEALTH CHECK
══════════════════════════════════════════════════════════════════════
Started:       2026-04-20 11:30:00
Total Devices: 3

──────────────────────────────────────────────────────────────────────
Device #1: core-switch (192.168.1.1)
──────────────────────────────────────────────────────────────────────
Type:     cisco
Status:   ✓ Online
Ping:     2ms

SNMP Info:
  Hostname: Core-SW-01
  Vendor:   cisco
  Uptime:   45 days, 12:34:56

══════════════════════════════════════════════════════════════════════
Summary:
  Total:   3 devices
  Online:  3 devices
  Failed:  0 devices
══════════════════════════════════════════════════════════════════════
```

---

### Backup Output

```
══════════════════════════════════════════════════════════════════════
           DEVICE CONFIGURATION BACKUP
══════════════════════════════════════════════════════════════════════
Started:       2026-04-20 11:30:00
Total Devices: 3

──────────────────────────────────────────────────────────────────────
Device #1: core-switch (192.168.1.1)
──────────────────────────────────────────────────────────────────────
Type:     cisco
Status:   ✓ Success
Saved to: backups/cisco/2026-04-20_11-30/Core-Switch.txt

══════════════════════════════════════════════════════════════════════
Summary:
  Total:     3 devices
  Success:   3 backups
  Failed:    0 devices
══════════════════════════════════════════════════════════════════════
```

---

### Exec Output

```
══════════════════════════════════════════════════════════════════════
Device #1: core-switch (192.168.1.1)
══════════════════════════════════════════════════════════════════════
Status: ✓ Success
──────────────────────────────────────────────────────────────────────
Cisco IOS Software, Version 15.2(4)E7
...

Building configuration...
[OK]
══════════════════════════════════════════════════════════════════════
Summary:
  Total:   3 devices
  Success: 3 devices
  Failed:  0 devices
══════════════════════════════════════════════════════════════════════
```

---

## Project Structure

```
.
├── cmd/
│   ├── cli/                # CLI commands
│   │   ├── root.go        # Root command & global flags
│   │   ├── monitor.go     # Health check command
│   │   ├── backup.go      # Backup command
│   │   ├── exec.go        # Bulk execution command
│   │   ├── diff.go        # Config comparison command
│   │   └── init.go        # Config initialization
│   └── bidar/
│       └── main.go        # Entry point
├── internal/
│   ├── backup/
│   │   ├── backup.go      # Backup logic & archiving
│   │   └── diff.go        # File comparison
│   ├── config/
│   │   ├── config.go      # Config loader (YAML/CSV auto-detect)
│   │   ├── csv.go         # CSV parser with settings
│   │   └── types.go       # Config structures
│   ├── device/
│   │   ├── check.go       # Health check (ping + SNMP)
│   │   ├── device.go      # Device methods
│   │   ├── exec.go        # Command execution
│   │   ├── ping.go        # ICMP ping
│   │   ├── ssh.go         # SSH connection & helpers
│   │   └── types.go       # Device structures
│   ├── logger/
│   │   └── logger.go      # Logging (console + file)
│   ├── report/
│   │   ├── report.go      # Report printing (monitor/backup)
│   │   ├── json.go        # JSON output
│   │   └── type.go        # Report structures
│   └── snmp/
│       ├── snmp.go        # SNMP queries
│       └── types.go       # SNMP structures
├── config.yaml            # YAML config (gitignored)
├── devices.csv            # CSV config (gitignored)
├── go.mod
├── go.sum
├── LICENSE
└── README.md

```

---

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Built with ❤️ using Go
