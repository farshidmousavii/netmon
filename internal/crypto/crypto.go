// Package crypto encrypts/decrypts secrets stored in Postgres bytea
// columns (snmp_profiles.community_encrypted, ssh_credentials.*, ...).
//
// It is used only by the DB-backed paths (import-devices, and later the
// daemon). The legacy YAML/CSV credential files in internal/config are
// intentionally NOT routed through this package — they stay plaintext on
// disk, operator-owned, exactly as before.
//
// Constructor-injected: an *Encryptor holds the key material; there is no
// package-level key or global. Commands that never touch encrypted data
// never construct an Encryptor and therefore never require BIDAR_MASTER_KEY.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// MasterKeyEnv is the environment variable holding the master key as
// base64-encoded 32 bytes. Pinned in docs/roadmap.md Phase 0.
const MasterKeyEnv = "BIDAR_MASTER_KEY"

// keySize is the AES-256 key length in bytes.
const keySize = 32

// nonceSize is the GCM standard nonce length in bytes.
const nonceSize = 12

// Format version byte. Layout of every encrypted value (single []byte,
// stored whole in a bytea column):
//
//	[0]      version byte: 0x01 = AES-256-GCM, 32-byte key
//	         from BIDAR_MASTER_KEY
//	[1:13]   12-byte random nonce, fresh per Encrypt call
//	[13:]    GCM ciphertext (plaintext + 16-byte authentication tag)
//
// The version byte exists so a future key rotation or algorithm change can
// add a new version (e.g. 0x02 with a new key) and let Decrypt switch on it,
// without a data migration to retrofit a version field.
const (
	versionAES256GCM byte = 0x01
)

// Encryptor seals and opens secrets with AES-256-GCM.
type Encryptor struct {
	aead cipher.AEAD
}

// New returns an Encryptor using key, which must be exactly 32 bytes
// (AES-256). It does not read any environment variable; use NewFromEnv for
// the BIDAR_MASTER_KEY path.
func New(key []byte) (*Encryptor, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes (AES-256), got %d", MasterKeyEnv, keySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	return &Encryptor{aead: aead}, nil
}

// NewFromEnv loads the master key from BIDAR_MASTER_KEY (base64-encoded
// 32 bytes) and constructs an Encryptor. It fails loudly when the variable
// is unset or malformed — a silently wrong key would encrypt secrets
// irrecoverably. Validation happens here, at the point of use, not at
// process startup: commands that never call this never fail on the env var.
func NewFromEnv() (*Encryptor, error) {
	raw := strings.TrimSpace(os.Getenv(MasterKeyEnv))
	if raw == "" {
		return nil, fmt.Errorf("%s is not set: set it to a base64-encoded %d-byte key (e.g. `openssl rand -base64 32`)", MasterKeyEnv, keySize)
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", MasterKeyEnv, err)
	}

	return New(key)
}

// Encrypt seals plaintext with a fresh random nonce and returns the
// versioned value (see layout comment above). The same plaintext yields a
// different value on every call.
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	sealed := e.aead.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, 1+nonceSize+len(sealed))
	out = append(out, versionAES256GCM)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt opens a value produced by Encrypt. It returns an error for
// unknown format versions, truncated/malformed values, and any
// authentication failure (wrong key or tampered ciphertext).
func (e *Encryptor) Decrypt(stored []byte) ([]byte, error) {
	if len(stored) < 1+nonceSize {
		return nil, fmt.Errorf("encrypted value too short (%d bytes): missing version and/or nonce", len(stored))
	}

	switch stored[0] {
	case versionAES256GCM:
		// fall through
	default:
		return nil, fmt.Errorf("unsupported encrypted value format version 0x%02x", stored[0])
	}

	if len(stored) < 1+nonceSize+e.aead.Overhead() {
		return nil, fmt.Errorf("encrypted value too short (%d bytes): cannot contain a GCM authentication tag", len(stored))
	}

	nonce := stored[1 : 1+nonceSize]
	ciphertext := stored[1+nonceSize:]

	plaintext, err := e.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt (wrong key or tampered value): %w", err)
	}
	return plaintext, nil
}
