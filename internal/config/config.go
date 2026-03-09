package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)



func LoadConfig(fileName string) (*Config, error) {

	cfg := Config{}

	file, err := os.ReadFile(fileName)
	if err != nil {
		return &cfg, fmt.Errorf("can not load config file : %w", err)
	}

	err = yaml.Unmarshal(file, &cfg)

	if err != nil {
		return &cfg, fmt.Errorf("Can not unmarshall : %w", err)
	}
	expandEnvVars(&cfg)
	return &cfg, nil

}

func LoadEnvVars() error {
	if err := godotenv.Load(".env"); err != nil {
		return fmt.Errorf("load .env file: %w", err)
	}
	return nil
}

func expandEnvVars(cfg *Config) {
	// credentials
	for name, cred := range cfg.Credentials {
		cred.Username = os.ExpandEnv(cred.Username)
		cred.Password = os.ExpandEnv(cred.Password)
		cfg.Credentials[name] = cred
	}

	// snmp
	cfg.SNMP.Community = os.ExpandEnv(cfg.SNMP.Community)
}


func (c *Config) GetCredential(name string) (CredentialInfo, error) {
    cred, ok := c.Credentials[name]
    if !ok {
        return CredentialInfo{}, fmt.Errorf("credential '%s' not found", name)
    }
    return cred, nil
}