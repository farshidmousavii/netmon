# Network Device Monitor

A CLI tool for monitoring and backing up network devices (Cisco & Mikrotik).

## Features

- ✅ Concurrent device monitoring with goroutines
- ✅ SSH-based configuration backup
- ✅ SNMP information gathering (hostname, uptime, vendor)
- ✅ Atomic backup with timestamped directories
- ✅ Structured logging (console + file)
- ✅ YAML configuration with environment variables

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
go build -o netmon
```

## Configuration

1. Copy example files:

```bash
cp configs/config.example.yaml config.yaml
cp .env.example .env
```

2. Edit `config.yaml` with your devices
3. Edit `.env` with your credentials

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
  directory: backup
  archive_path: ""
```

Create a `.env` file:

```env
CISCO_PASSWORD=yourpassword
MIKROTIK_PASSWORD=yourpassword
SNMP_COMMUNITY=public
```

## Usage

```bash
# Run with console output
./netmon

# Run with file logging
./netmon --log-file
```

## Output

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
  ✓ Saved to: backup/cisco/2025-03-05_14-30-00/Core-SW-01.txt

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
  ✓ Saved to: backup/mikrotik/2025-03-05_14-30-00/Edge-Router-01.rsc

═══════════════════════════════════════════════════════════
Summary:
  Total:   2 devices
  Online:  2 devices
  Failed:  0 devices
═══════════════════════════════════════════════════════════
```

## Requirements

- Go 1.21+
- SSH access to devices
- SNMP enabled (optional)

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
