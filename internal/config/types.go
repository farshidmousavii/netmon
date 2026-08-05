package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Devices     []DeviceConfig            `yaml:"devices"`
	SNMP        *SNMPConfig               `yaml:"snmp,omitempty"`
	Backup      BackupConfig              `yaml:"backup"`
	Credentials map[string]CredentialInfo `yaml:"credentials"`
	SSH         *SSHSettings              `yaml:"ssh,omitempty"`
	Version     int                       `yaml:"version,omitempty"`
}

type DeviceConfig struct {
	Name       string `yaml:"name"`
	IP         string `yaml:"ip"`
	Port       string `yaml:"port"`
	Vendor     string `yaml:"vendor"`
	Credential string `yaml:"credential"`
	Type       string `yaml:"type,omitempty"` // router, switch, firewall, etc.
}

type CredentialInfo struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type SNMPConfig struct {
	Community string `yaml:"community"`
	Timeout   int    `yaml:"timeout"`
}

type BackupConfig struct {
	Directory   string `yaml:"directory"`
	ArchivePath string `yaml:"archive_path"`
}

type SSHSettings struct {
	Timeout int           `yaml:"timeout"`
	Retry   RetrySettings `yaml:"retry,omitempty"`
}

type RetrySettings struct {
	MaxAttempts  int     `yaml:"max_attempts"`
	InitialDelay int     `yaml:"initial_delay"`
	MaxDelay     int     `yaml:"max_delay"`
	Multiplier   float64 `yaml:"multiplier"`
}

// DefaultSSHSettings
func DefaultSSHSettings() *SSHSettings {
	return &SSHSettings{
		Timeout: 10,
		Retry: RetrySettings{
			MaxAttempts:  3,
			InitialDelay: 1,
			MaxDelay:     10,
			Multiplier:   2.0,
		},
	}
}

func (c *Config) GetSSHSettings() *SSHSettings {
	if c.SSH != nil {
		return c.SSH
	}
	return DefaultSSHSettings()
}

// Save - write config back to disk atomically with a .bak backup.
// Writes to a temp file then renames (never a half-written config).
func (c *Config) Save(path string) error {
	if c.Version == 0 {
		c.Version = 1
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// backup existing file before overwrite
	if _, err := os.Stat(path); err == nil {
		bak := path + ".bak"
		if err := copyFile(path, bak); err != nil {
			return fmt.Errorf("backup config: %w", err)
		}
	}

	// atomic write: temp + rename
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
