# Network Device Monitor

A CLI tool for monitoring and backing up network devices (Cisco & Mikrotik).

## Features

- ✅ Concurrent device monitoring with goroutines
- ✅ SSH-based configuration backup
- ✅ SNMP information gathering (hostname, uptime, vendor)
- ✅ Atomic backup with timestamped directories
- ✅ Structured logging (console + file)
- ✅ YAML configuration with environment variables

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
...
```

## Requirements

- Go 1.21+
- SSH access to devices
- SNMP enabled (optional)

## License

MIT
