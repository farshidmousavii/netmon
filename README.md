# NetMon

NetMon is a powerful CLI tool for network device monitoring, configuration backup, and bulk command execution across Cisco and MikroTik devices.

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
git clone https://github.com/farshidmousavii/netmon.git
cd netmon
go build -o netmon-cli ./cmd/netmon
```

Run Directly

```bash
go run ./cmd/netmon [command]
```

## Quick Start

1. Initialize Configuration
   YAML format (recommended for shared credentials):

```bash
# Creates config.yaml
./netmon-cli init
```

CSV format (recommended for bulk import):

```bash
./netmon-cli init --format csv
```

2. Edit Configuration

YAML (`config.yaml`):

```yaml
version: 1

credentials:
  cisco:
    username: admin
    password: cisco123

  mikrotik:
    username: admin
    password: mikrotik123

devices:
  - name: core-switch
    ip: 192.168.1.1
    port: 22
    vendor: cisco
    credential: cisco

snmp:
  community: public
  timeout: 10

backup:
  directory: backups
  archive_path: ""
```

CSV (`devices.csv`):

```csv
#snmp_community=<value> — SNMP community string (default: public)
#snmp_timeout=<seconds> — SNMP timeout (default: 10)
#backup_dir=<path> — Backup directory (default: backups)
#backup_archive=<path> — Archive path for old backups
name,ip,port,vendor,username,password
core-switch,192.168.1.1,22,cisco,admin,cisco123
dist-switch-01,192.168.2.1,22,cisco,admin,cisco123
access-sw-01,192.168.3.1,22,cisco,admin,cisco123
edge-router,192.168.4.1,22,mikrotik,admin,mikrotik123
```

```
CSV Settings (optional):
#snmp_community=<value> — SNMP community string (default: public)
#snmp_timeout=<seconds> — SNMP timeout (default: 10)
#backup_dir=<path> — Backup directory (default: backups)
#backup_archive=<path> — Archive path for old backups

```

3. Run Commands

```bash#
Monitor with YAML
./netmon-cli monitor

# Monitor with CSV
./netmon-cli monitor --config devices.csv

# Backup only
./netmon-cli backup

# Execute bulk commands
./netmon-cli exec --type cisco -c "show version"
```

# Commands

## monitor

Health check devices with ping and SNMP.

```bash
# Basic monitoring
./netmon-cli monitor

# Skip backup during monitoring
./netmon-cli monitor --skip-backup

# Skip SNMP queries
./netmon-cli monitor --skip-snmp

# JSON output
./netmon-cli monitor --json

# Enable file logging
./netmon-cli monitor --log

# Override SNMP settings
./netmon-cli monitor --snmp-community private --snmp-timeout 20

# Override backup directory
./netmon-cli monitor --backup-dir /opt/backups


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
./netmon-cli backup

# With logging
./netmon-cli backup --log

# JSON output
./netmon-cli backup --json
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
./netmon-cli exec -d core-switch -c "show ip interface brief"
#or
./netmon-cli exec -d 192.168.1.1 -c "show ip interface brief"

# All Cisco devices - config command
./netmon-cli exec --type cisco -c "interface gi0/1" -c "description UPLINK"

# With config save (Cisco only)
./netmon-cli exec -d core-switch -c "interface gi0/1" -c "shutdown" --save

# Dry run (preview without execution)
./netmon-cli exec --type cisco -c "interface gi0/1" -c "shutdown" --dry-run

# Save output to file
./netmon-cli exec --type cisco -c "show running-config" -o output.txt

# Interactive mode
./netmon-cli exec --type cisco
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
./netmon-cli exec -d core-switch -c "show ip route"

# Config commands (auto-detected, enters config mode)
./netmon-cli exec --type cisco \
  -c "interface gi0/1" \
  -c "description UPLINK_TO_CORE" \
  -c "no shutdown" \
  --save

# Dry run to preview
./netmon-cli exec --type cisco \
  -c "interface gi0/1" \
  -c "shutdown" \
  --dry-run

# Execute and save output
./netmon-cli exec --type cisco -c "show run" -o configs.txt
```

# diff

Compare two backup files.

```bash
./netmon-cli diff backups/cisco/2026-04-20_10-00/Core-Switch.txt \
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
./netmon-cli init

# Create CSV config
./netmon-cli init --format csv
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
./netmon-cli monitor --config devices.csv
./netmon-cli backup --config /etc/netmon/config.yaml
./netmon-cli exec --config production.csv --type cisco -c "show version"
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
./netmon-cli monitor

# Monitor with CSV, override SNMP settings
./netmon-cli monitor --config devices.csv \
  --snmp-community private \
  --snmp-timeout 20

# Monitor without SNMP
./netmon-cli monitor --skip-snmp

# Monitor with logging and JSON output
./netmon-cli monitor --log --json > report.json
```

## Backup

```bash
# Backup all devices
./netmon-cli backup

# Backup with custom directory
./netmon-cli monitor --backup-dir /mnt/backups

# Backup with archiving
./netmon-cli monitor --backup-archive /mnt/archive
```

## Bulk Execution

```bash
# Check version on all Cisco devices
./netmon-cli exec --type cisco -c "show version"

# Configure interface on specific device
./netmon-cli exec -d core-switch \
  -c "interface gi0/1" \
  -c "description UPLINK_TO_DATACENTER" \
  -c "no shutdown" \
  --save

# Dry run before execution
./netmon-cli exec --type cisco \
  -c "no ip http server" \
  -c "no ip http secure-server" \
  --dry-run

# Interactive mode
./netmon-cli exec --type cisco
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
./netmon-cli diff \
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
│   └── netmon/
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
