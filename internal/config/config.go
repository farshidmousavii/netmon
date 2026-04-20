package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfig - Auto detect CSV or Yaml
func LoadConfig(path string) (*Config, error) {
	// check if file exist
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("config file not found: %s", path)
	}

	// detect file extention
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".yaml", ".yml":
		return loadFromYAML(path)
	case ".csv":
		return loadFromCSV(path)
	default:
		return nil, fmt.Errorf("unsupported config format: %s (use .yaml or .csv)", ext)
	}
}

// loadFromYAML
func loadFromYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read YAML file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Validation
	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("no devices configured in YAML")
	}

	if len(cfg.Credentials) == 0 {
		return nil, fmt.Errorf("no credentials configured in YAML")
	}

	return &cfg, nil
}

func loadFromCSV(path string) (*Config, error) {
	devices, credentials, snmpConfig, backupConfig, err := parseCSV(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Devices:     devices,
		Credentials: credentials,
		SNMP:        snmpConfig,
		Backup:      backupConfig,
	}

	return cfg, nil
}

func (c *Config) GetCredential(name string) (CredentialInfo, error) {
	cred, ok := c.Credentials[name]
	if !ok {
		return CredentialInfo{}, fmt.Errorf("credential '%s' not found", name)
	}
	return cred, nil
}
