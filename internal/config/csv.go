package config

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parseCSV - parse CSV file
func parseCSV(csvPath string) ([]DeviceConfig, map[string]CredentialInfo, error) {
	file, err := os.Open(csvPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	//reading header
	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV header: %w", err)
	}

	// Normalize header
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(header[i]))
	}

	// checking header
	expectedHeader := []string{"name", "ip", "port", "vendor", "username", "password"}
	if !equalSlices(header, expectedHeader) {
		return nil, nil, fmt.Errorf("invalid CSV header\nExpected: %v\nGot: %v", expectedHeader, header)
	}

	// reading rows
	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV records: %w", err)
	}

	var devices []DeviceConfig
	credentials := make(map[string]CredentialInfo)

	for i, record := range records {
		lineNum := i + 2

		// Skip empty lines
		if len(record) == 0 || (len(record) == 1 && strings.TrimSpace(record[0]) == "") {
			continue
		}

		if len(record) != 6 {
			return nil, nil, fmt.Errorf("line %d: expected 6 columns, got %d", lineNum, len(record))
		}

		// Clean data
		name := strings.TrimSpace(record[0])
		ip := strings.TrimSpace(record[1])
		port := strings.TrimSpace(record[2])
		vendor := strings.TrimSpace(record[3])
		username := strings.TrimSpace(record[4])
		password := strings.TrimSpace(record[5])

		// Validation
		if name == "" {
			return nil, nil, fmt.Errorf("line %d: device name cannot be empty", lineNum)
		}
		if ip == "" {
			return nil, nil, fmt.Errorf("line %d: IP address cannot be empty", lineNum)
		}

		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return nil, nil, fmt.Errorf("line %d: invalid port '%s'", lineNum, port)
		}

		if vendor == "" {
			return nil, nil, fmt.Errorf("line %d: vendor cannot be empty", lineNum)
		}
		if username == "" {
			return nil, nil, fmt.Errorf("line %d: username cannot be empty", lineNum)
		}
		if password == "" {
			return nil, nil, fmt.Errorf("line %d: password cannot be empty", lineNum)
		}

		// create credential name unique
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
		return nil, nil, fmt.Errorf("no valid devices found in CSV")
	}

	return devices, credentials, nil
}

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
