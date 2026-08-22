package snmp

// Mapping from an snmp_profiles row (docs/database-schema.md) to a Client
// Config — the one shared translation both Phase 2 providers (and any
// later consumer) use, so protocol-name handling can't drift apart.
//
// Secrets arrive already decrypted: this package never sees ciphertext.
// The caller's flow is store.GetSNMPProfile -> internal/crypto decrypt ->
// Profile here.

import (
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Profile carries one snmp_profiles row's connection-relevant fields with
// secrets already decrypted by the caller.
type Profile struct {
	Version        string // "v2c" | "v3"
	Community      string // v2c only
	V3Username     string
	V3AuthProtocol string // "" | NOAUTH | MD5 | SHA | SHA224 | SHA256 | SHA384 | SHA512
	V3AuthKey      string
	V3PrivProtocol string // "" | NOPRIV | DES | AES | AES192 | AES192C | AES256 | AES256C
	V3PrivKey      string
	TimeoutMS      int
	Retries        int
}

// ConfigFromProfile maps a decrypted profile onto a Config for target.
// Zero TimeoutMS/Retries pass through and take NewClient's defaults.
//
// Validation beyond protocol-name mapping is NewClient's job; the one
// check done here is v2c-without-community, which would otherwise only
// surface as an agent-side rejection after dialing.
func ConfigFromProfile(target string, p Profile) (Config, error) {
	cfg := Config{
		Target:  target,
		Timeout: time.Duration(p.TimeoutMS) * time.Millisecond,
		Retries: p.Retries,
	}

	switch strings.ToLower(strings.TrimSpace(p.Version)) {
	case "v2c":
		cfg.Version = gosnmp.Version2c
		cfg.Community = p.Community
		if cfg.Community == "" {
			return Config{}, fmt.Errorf("snmp profile: v2c profile has no community")
		}
	case "v3":
		cfg.Version = gosnmp.Version3
		auth, err := ParseAuthProtocol(p.V3AuthProtocol)
		if err != nil {
			return Config{}, fmt.Errorf("snmp profile: %w", err)
		}
		priv, err := ParsePrivProtocol(p.V3PrivProtocol)
		if err != nil {
			return Config{}, fmt.Errorf("snmp profile: %w", err)
		}
		cfg.Security = &V3Config{
			Username:     p.V3Username,
			AuthProtocol: auth,
			AuthKey:      p.V3AuthKey,
			PrivProtocol: priv,
			PrivKey:      p.V3PrivKey,
		}
	default:
		return Config{}, fmt.Errorf("snmp profile: unsupported version %q (use v2c or v3)", p.Version)
	}
	return cfg, nil
}
