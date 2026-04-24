package config

type Config struct {
	Devices     []DeviceConfig            `yaml:"devices"`
	SNMP        *SNMPConfig               `yaml:"snmp,omitempty"`
	Backup      BackupConfig              `yaml:"backup"`
	Credentials map[string]CredentialInfo `yaml:"credentials"`
	SSH         *SSHSettings              `yaml:"ssh,omitempty"`
}

type DeviceConfig struct {
	Name       string `yaml:"name"`
	IP         string `yaml:"ip"`
	Port       string `yaml:"port"`
	Vendor     string `yaml:"vendor"`
	Credential string `yaml:"credential"`
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
