package config

type Config struct {
	Devices     []DeviceConfig            `yaml:"devices"`
	SNMP        *SNMPConfig               `yaml:"snmp"`
	Backup      BackupConfig              `yaml:"backup"`
	Credentials map[string]CredentialInfo `yaml:"credentials"`
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