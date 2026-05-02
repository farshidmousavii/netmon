package config

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// Validate - validation config
func (c *Config) Validate() error {

	if c == nil {
		return fmt.Errorf("config is null")
	}

	if len(c.Devices) == 0 {
		return fmt.Errorf("no devices configured")
	}

	if len(c.Credentials) == 0 {
		return fmt.Errorf("no credentials configured")
	}

	// Validate devices
	deviceNames := make(map[string]bool)
	for i, device := range c.Devices {
		if err := device.Validate(); err != nil {
			return fmt.Errorf("device #%d (%s): %w", i+1, device.Name, err)
		}

		// duplicate names
		if deviceNames[device.Name] {
			return fmt.Errorf("duplicate device name: %s", device.Name)
		}
		deviceNames[device.Name] = true

		//credential existence
		if _, exists := c.Credentials[device.Credential]; !exists {
			return fmt.Errorf("device %s: credential '%s' not found", device.Name, device.Credential)
		}
	}

	// Validate credentials
	for name, cred := range c.Credentials {
		if err := cred.Validate(); err != nil {
			return fmt.Errorf("credential '%s': %w", name, err)
		}
	}

	// Validate SNMP
	if c.SNMP != nil {
		if err := c.SNMP.Validate(); err != nil {
			return fmt.Errorf("SNMP config: %w", err)
		}
	}

	// Validate Backup
	if err := c.Backup.Validate(); err != nil {
		return fmt.Errorf("backup config: %w", err)
	}

	// Validate SSH settings
	if c.SSH != nil {
		if err := c.SSH.Validate(); err != nil {
			return fmt.Errorf("SSH config: %w", err)
		}
	}

	return nil
}

// Validate - validation device config
func (d *DeviceConfig) Validate() error {
	// Name
	if d.Name == "" {
		return fmt.Errorf("device name cannot be empty")
	}
	if len(d.Name) > 255 {
		return fmt.Errorf("device name too long (max 255 chars)")
	}
	if strings.Contains(d.Name, "/") || strings.Contains(d.Name, "\\") {
		return fmt.Errorf("device name cannot contain path separators")
	}

	// IP
	if err := validateIP(d.IP); err != nil {
		return err
	}

	// Port
	if err := validatePort(d.Port); err != nil {
		return err
	}

	// Vendor
	if err := validateVendor(d.Vendor); err != nil {
		return err
	}

	// Credential
	if d.Credential == "" {
		return fmt.Errorf("credential cannot be empty")
	}

	return nil
}

// Validate - validation credential
func (c *CredentialInfo) Validate() error {
	if c.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(c.Username) > 255 {
		return fmt.Errorf("username too long (max 255 chars)")
	}

	if c.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	if len(c.Password) > 255 {
		return fmt.Errorf("password too long (max 255 chars)")
	}

	return nil
}

// Validate - validation SNMP config
func (s *SNMPConfig) Validate() error {
	if s.Community == "" {
		return fmt.Errorf("SNMP community cannot be empty")
	}

	if s.Timeout < 1 || s.Timeout > 300 {
		return fmt.Errorf("SNMP timeout must be between 1 and 300 seconds (got %d)", s.Timeout)
	}

	return nil
}

// Validate - validation backup config
func (b *BackupConfig) Validate() error {
	if b.Directory == "" {
		return fmt.Errorf("backup directory cannot be empty")
	}

	// check path traversal
	if strings.Contains(b.Directory, "..") {
		return fmt.Errorf("backup directory cannot contain '..'")
	}

	// Optional archive path
	if b.ArchivePath != "" && strings.Contains(b.ArchivePath, "..") {
		return fmt.Errorf("archive path cannot contain '..'")
	}

	return nil
}

// Validate - validation SSH settings
func (s *SSHSettings) Validate() error {
	// Timeout
	if s.Timeout < 1 || s.Timeout > 300 {
		return fmt.Errorf("SSH timeout must be between 1 and 300 seconds (got %d)", s.Timeout)
	}

	// Retry settings
	if err := s.Retry.Validate(); err != nil {
		return fmt.Errorf("retry settings: %w", err)
	}

	return nil
}

// Validate - validation retry settings
func (r *RetrySettings) Validate() error {
	// Max attempts
	if r.MaxAttempts < 1 || r.MaxAttempts > 10 {
		return fmt.Errorf("max attempts must be between 1 and 10 (got %d)", r.MaxAttempts)
	}

	// Initial delay
	if r.InitialDelay < 0 || r.InitialDelay > 60 {
		return fmt.Errorf("initial delay must be between 0 and 60 seconds (got %d)", r.InitialDelay)
	}

	// Max delay
	if r.MaxDelay < r.InitialDelay {
		return fmt.Errorf("max delay (%d) cannot be less than initial delay (%d)", r.MaxDelay, r.InitialDelay)
	}

	if r.MaxDelay > 300 {
		return fmt.Errorf("max delay must be at most 300 seconds (got %d)", r.MaxDelay)
	}

	// Multiplier
	if r.Multiplier < 1.0 || r.Multiplier > 10.0 {
		return fmt.Errorf("multiplier must be between 1.0 and 10.0 (got %.2f)", r.Multiplier)
	}

	return nil
}

// Helper functions

func validateIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address cannot be empty")
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	// Block localhost/multicast
	if parsed.IsLoopback() {
		return fmt.Errorf("localhost IP not allowed: %s", ip)
	}
	if parsed.IsMulticast() {
		return fmt.Errorf("multicast IP not allowed: %s", ip)
	}

	return nil
}

func validatePort(port string) error {
	if port == "" {
		return fmt.Errorf("port cannot be empty")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port number: %s", port)
	}

	if portNum < 1 || portNum > 65535 {
		return fmt.Errorf("port must be between 1 and 65535 (got %d)", portNum)
	}

	return nil
}

func validateVendor(vendor string) error {
	if vendor == "" {
		return fmt.Errorf("vendor cannot be empty")
	}

	vendor = strings.ToLower(vendor)
	supportedVendors := []string{"cisco", "mikrotik"}

	if slices.Contains(supportedVendors, vendor) {
		return nil
	}

	return fmt.Errorf("unsupported vendor: %s (supported: %v)", vendor, supportedVendors)
}
