package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// testKey returns a fresh 32-byte key, so every test run uses new material.
func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return key
}

func mustEncryptor(t *testing.T, key []byte) *Encryptor {
	t.Helper()
	e, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestNewRejectsWrongKeyLength(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		key := make([]byte, n)
		if _, err := New(key); err == nil {
			t.Errorf("New with %d-byte key: expected error, got nil", n)
		}
	}

	if _, err := New(make([]byte, keySize)); err != nil {
		t.Errorf("New with %d-byte key: unexpected error: %v", keySize, err)
	}
}

func TestNewFromEnv(t *testing.T) {
	key := testKey(t)
	encoded := base64.StdEncoding.EncodeToString(key)

	t.Run("unset", func(t *testing.T) {
		t.Setenv(MasterKeyEnv, "")
		if _, err := NewFromEnv(); err == nil {
			t.Fatal("expected error when env unset")
		}
	})

	t.Run("invalid base64", func(t *testing.T) {
		t.Setenv(MasterKeyEnv, "%%%not-base64%%%")
		if _, err := NewFromEnv(); err == nil {
			t.Fatal("expected error for invalid base64")
		}
	})

	t.Run("wrong length key", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString(key[:16])
		t.Setenv(MasterKeyEnv, short)
		_, err := NewFromEnv()
		if err == nil {
			t.Fatal("expected error for 16-byte key")
		}
		if !strings.Contains(err.Error(), "32 bytes") {
			t.Errorf("error should explain the key length requirement, got: %v", err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Setenv(MasterKeyEnv, encoded)
		if _, err := NewFromEnv(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRoundTrip(t *testing.T) {
	e := mustEncryptor(t, testKey(t))

	plaintexts := [][]byte{
		nil,
		{},
		[]byte("public"),
		[]byte("Center@CoreSW"),
		[]byte("a very long secret value padded out well beyond the 16-byte GCM tag size to exercise block-boundary handling in the cipher"),
		bytes.Repeat([]byte{0xAB}, 4096),
	}

	for _, pt := range plaintexts {
		ct, err := e.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%d bytes): %v", len(pt), err)
		}

		// Layout: 1 version byte + 12 nonce + ciphertext(>=16 tag).
		if len(ct) != 1+nonceSize+len(pt)+e.aead.Overhead() {
			t.Errorf("Encrypt(%d bytes): unexpected length %d", len(pt), len(ct))
		}
		if ct[0] != versionAES256GCM {
			t.Errorf("first byte should be version 0x%02x, got 0x%02x", versionAES256GCM, ct[0])
		}

		got, err := e.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt(%d bytes): %v", len(pt), err)
		}
		if !bytes.Equal(got, pt) {
			t.Errorf("round-trip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	e := mustEncryptor(t, testKey(t))

	pt := []byte("same plaintext twice")
	ct1, err := e.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	ct2, err := e.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext produced identical values; nonce is not randomized")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	e1 := mustEncryptor(t, testKey(t))
	e2 := mustEncryptor(t, testKey(t))

	ct, err := e1.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := e2.Decrypt(ct); err == nil {
		t.Fatal("expected error decrypting with a different key")
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	e := mustEncryptor(t, testKey(t))

	ct, err := e.Encrypt([]byte("sensitive data"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Tamper every byte region: nonce area, ciphertext body, auth tag.
	regions := []int{
		1,                 // first nonce byte
		1 + nonceSize - 1, // last nonce byte
		1 + nonceSize,     // first ciphertext byte
		1 + nonceSize + 8, // middle of ciphertext
		len(ct) - 1,       // last tag byte
	}
	for _, i := range regions {
		bad := append([]byte(nil), ct...)
		bad[i] ^= 0xFF
		if _, err := e.Decrypt(bad); err == nil {
			t.Errorf("expected error for tampered byte at offset %d", i)
		}
	}

	// Truncation must also fail (tag is cut off / short).
	if _, err := e.Decrypt(ct[:len(ct)-1]); err == nil {
		t.Error("expected error for truncated ciphertext")
	}
}

func TestDecryptMalformedInputReturnsErrorNotPanic(t *testing.T) {
	e := mustEncryptor(t, testKey(t))

	cases := [][]byte{
		nil,
		{},
		{versionAES256GCM},       // version only
		{versionAES256GCM, 0x00}, // version + 1 byte, no nonce
		{0x02},                   // unknown version, short
		append([]byte{0x02}, make([]byte, 64)...),                         // unknown version, plausible length
		append([]byte{versionAES256GCM}, make([]byte, 1+nonceSize+10)...), // nonce + truncated ciphertext (no tag)
		bytes.Repeat([]byte{0x7F}, 128),                                   // garbage with a bogus version byte
	}

	for i, tc := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d: Decrypt panicked: %v", i, r)
				}
			}()
			got, err := e.Decrypt(tc)
			if err == nil {
				t.Errorf("case %d: expected error, got plaintext %q", i, got)
			}
		}()
	}
}

func TestDecryptUnknownVersionFails(t *testing.T) {
	e := mustEncryptor(t, testKey(t))

	// Valid-looking value with a bogus version byte.
	ct, err := e.Encrypt([]byte("x"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct[0] = 0x7F

	if _, err := e.Decrypt(ct); err == nil {
		t.Fatal("expected error for unknown format version")
	}
}
