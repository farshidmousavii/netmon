package config

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parseCSV - support reading settings + CSV
func parseCSV(csvPath string) ([]DeviceConfig, map[string]CredentialInfo, *SNMPConfig, BackupConfig, error) {
	file, err := os.Open(csvPath)
	if err != nil {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("open CSV file: %w", err)
	}
	defer file.Close()

	// Default settings
	snmpConfig := &SNMPConfig{
		Community: "public",
		Timeout:   10,
	}
	backupConfig := BackupConfig{
		Directory:   "backups",
		ArchivePath: "",
	}

	// --- Phase 1: read line-by-line ---
	scanner := bufio.NewScanner(file)
	var csvLines []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			parseSetting(line, snmpConfig, &backupConfig)
			continue
		}

		csvLines = append(csvLines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("scan file: %w", err)
	}

	if len(csvLines) == 0 {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("no CSV data found")
	}

	// --- Phase 2: parse CSV only ---
	reader := csv.NewReader(strings.NewReader(strings.Join(csvLines, "\n")))
	reader.TrimLeadingSpace = true

	allLines, err := reader.ReadAll()
	if err != nil {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("read CSV: %w", err)
	}

	if len(allLines) == 0 {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("CSV file is empty")
	}

	// --- Header ---
	headerLine := allLines[0]
	for i := range headerLine {
		headerLine[i] = strings.ToLower(strings.TrimSpace(headerLine[i]))
	}

	expectedHeader := []string{"name", "ip", "port", "vendor", "username", "password"}
	if !equalSlices(headerLine, expectedHeader) {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf(
			"invalid CSV header\nExpected: %v\nGot: %v",
			expectedHeader, headerLine,
		)
	}

	// --- Data ---
	var devices []DeviceConfig
	credentials := make(map[string]CredentialInfo)

	for i, record := range allLines[1:] {
		lineNum := i + 2 // +1 for header +1 for index

		if len(record) != 6 {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf(
				"line %d: expected 6 columns, got %d",
				lineNum, len(record),
			)
		}

		name := strings.TrimSpace(record[0])
		ip := strings.TrimSpace(record[1])
		port := strings.TrimSpace(record[2])
		vendor := strings.TrimSpace(record[3])
		username := strings.TrimSpace(record[4])
		password := strings.TrimSpace(record[5])

		if name == "" {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: device name cannot be empty", lineNum)
		}
		if ip == "" {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: IP cannot be empty", lineNum)
		}

		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: invalid port '%s'", lineNum, port)
		}

		if vendor == "" {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: vendor cannot be empty", lineNum)
		}
		if username == "" {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: username cannot be empty", lineNum)
		}
		if password == "" {
			return nil, nil, nil, BackupConfig{}, fmt.Errorf("line %d: password cannot be empty", lineNum)
		}

		credName := fmt.Sprintf("csv_%s", name)
		credentials[credName] = CredentialInfo{
			Username: username,
			Password: password,
		}

		devices = append(devices, DeviceConfig{
			Name:       name,
			IP:         ip,
			Port:       port,
			Vendor:     vendor,
			Credential: credName,
		})
	}

	if len(devices) == 0 {
		return nil, nil, nil, BackupConfig{}, fmt.Errorf("no devices found in CSV")
	}

	return devices, credentials, snmpConfig, backupConfig, nil
}

// --- Settings parser ---
func parseSetting(line string, snmp *SNMPConfig, backup *BackupConfig) {
	line = strings.TrimPrefix(line, "#")
	line = strings.TrimSpace(line)

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	if value == "" {
		return
	}

	switch key {
	case "snmp_community":
		snmp.Community = value
	case "snmp_timeout":
		if timeout, err := strconv.Atoi(value); err == nil && timeout > 0 {
			snmp.Timeout = timeout
		}
	case "backup_dir":
		backup.Directory = value
	case "backup_archive":
		backup.ArchivePath = value
	}
}

// --- utils ---
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
