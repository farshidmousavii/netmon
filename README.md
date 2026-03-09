# Network Device Monitor

A CLI tool for monitoring and backing up network devices (Cisco & Mikrotik).

## Features

- ✅ Concurrent device monitoring with goroutines
- ✅ SSH-based configuration backup
- ✅ SNMP information gathering (hostname, uptime, vendor)
- ✅ Atomic backup with timestamped directories
- ✅ Structured logging (console + file)
- ✅ YAML configuration with environment variables
- ✅ JSON output support
- ✅ Configuration diff tool
- ✅ Flexible command-line flags

## Limitations

- Currently supports only Cisco and Mikrotik devices
- Requires SSH access with password authentication (key-based auth not supported yet)
- SNMP must be enabled on devices (optional but recommended)
- Sequential backup per device (no batch operations)
- No GUI or web interface

## Installation

```bash
git clone https://github.com/farshidmousavii/netmon.git
cd netmon
go build -o netmon-cli ./cmd/netmon
```

## Configuration

1. Copy example files:

```bash
cp configs/config.example.yaml config.yaml
cp .env.example .env
```

2. Edit `config.yaml` with your devices

```yaml
version: 1

credentials:
  default:
    username: admin
    password: ${DEVICE_PASSWORD}

  cisco:
    username: admin
    password: ${CISCO_PASSWORD}

  mikrotik:
    username: admin
    password: ${MIKROTIK_PASSWORD}

devices:
  - name: core-switch
    ip: 192.168.2.1
    port: 22
    vendor: cisco
    credential: cisco

  - name: edge-router
    ip: 192.168.2.2
    port: 22
    vendor: mikrotik
    credential: mikrotik

snmp:
  community: ${SNMP_COMMUNITY}
  timeout: 10

backup:
  directory: backups
  archive_path: ""
```

3. Edit `.env` with your credentials

```env
CISCO_PASSWORD=yourpassword
MIKROTIK_PASSWORD=yourpassword
SNMP_COMMUNITY=public
```

## Usage

### Monitoring

```bash
# Basic run with console output
./netmon-cli

# Enable file logging
./netmon-cli -l

# Skip backup (health check only)
./netmon-cli --skip-backup

# Skip SNMP queries
./netmon-cli --skip-snmp

# Output as JSON
./netmon-cli --json

# Custom config file
./netmon-cli --config /path/to/config.yaml

# Combine flags
./netmon-cli -l --skip-backup
```

### Configuration Diff

Compare two backup files to see what changed:

```bash
./netmon-cli diff <file1> <file2>

# Example
./netmon-cli diff backups/cisco/2025-03-05_14-30-00/Core-SW-01.txt \
                  backups/cisco/2025-03-06_14-30-00/Core-SW-01.txt
```

### Available Flags

| Flag            | Description                                |
| --------------- | ------------------------------------------ |
| `-l`            | Enable file logging                        |
| `--config`      | Path to config file (default: config.yaml) |
| `--skip-backup` | Skip backup, only health check             |
| `--skip-snmp`   | Skip SNMP queries                          |
| `--json`        | Output report as JSON                      |

## Output

### Console Report

```
═══════════════════════════════════════════════════════════
           NETWORK DEVICE MONITORING REPORT
═══════════════════════════════════════════════════════════
Started:       2025-03-05 14:30:00
Total Devices: 2

──────────────────────────────────────────────────────────
Device #1: core-switch (192.168.2.1)
──────────────────────────────────────────────────────────
Type:     cisco
Status:   ✓ Online
Ping:     2ms

SNMP Info:
  Hostname: Core-SW-01
  Vendor:   cisco
  Uptime:   45 days, 12:34:56

Backup:
  ✓ Saved to: backups/cisco/2025-03-05_14-30-00/Core-SW-01.txt

──────────────────────────────────────────────────────────
Device #2: edge-router (192.168.2.2)
──────────────────────────────────────────────────────────
Type:     mikrotik
Status:   ✓ Online
Ping:     1ms

SNMP Info:
  Hostname: Edge-Router-01
  Vendor:   mikrotik
  Uptime:   12 days, 08:15:30

Backup:
  ✓ Saved to: backups/mikrotik/2025-03-05_14-30-00/Edge-Router-01.rsc

═══════════════════════════════════════════════════════════
Summary:
  Total:   2 devices
  Online:  2 devices
  Failed:  0 devices
═══════════════════════════════════════════════════════════
```

### JSON Output

```bash
./netmon-cli --json
```

Generates a timestamped JSON report in `reports/` directory:

```json
[
  {
    "Name": "core-switch",
    "IP": "192.168.2.1",
    "Type": "cisco",
    "Online": true,
    "PingTime": "2ms",
    "SNMPInfo": {
      "Hostname": "Core-SW-01",
      "Vendor": "cisco",
      "Uptime": "45 days, 12:34:56"
    },
    "BackupPath": "backups/cisco/2025-03-05_14-30-00/Core-SW-01.txt",
    "Error": null
  },
  {
    "Name": "edge-router",
    "IP": "192.168.2.2",
    "Type": "mikrotik",
    "Online": true,
    "PingTime": "1ms",
    "SNMPInfo": {
      "Hostname": "Edge-Router-01",
      "Vendor": "mikrotik",
      "Uptime": "12 days, 08:15:30"
    },
    "BackupPath": "backups/mikrotik/2025-03-05_14-30-00/Edge-Router-01.rsc",
    "Error": null
  }
]
```

### Diff Output

```
Found 3 differences:

Line 15:
  - hostname OLD-NAME
  + hostname NEW-NAME

Line 42:
  - ip address 192.168.1.1 255.255.255.0
  + ip address 192.168.2.1 255.255.255.0

Line 89:
  - ntp server 10.0.0.1
  + ntp server 10.0.0.2
```

## Requirements

- Go 1.21+
- SSH access to devices
- SNMP enabled (optional)

## Project Structure

```
netmon/
├── cmd/
│   └── netmon/           # Main entry point
├── internal/
│   ├── backup/           # Backup and diff logic
│   ├── config/           # Configuration management
│   ├── device/           # Device operations
│   ├── logger/           # Structured logging
│   ├── report/           # Report generation
│   └── snmp/             # SNMP operations
├── configs/
│   └── config.example.yaml
├── .env.example
└── README.md
```

## License

MIT

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
