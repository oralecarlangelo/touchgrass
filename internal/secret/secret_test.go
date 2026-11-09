package secret

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKeyHex is a fixed 32-byte key for round-trip tests.
const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// otherKeyHex differs from testKeyHex in its last byte.
const otherKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e20"

func mustKey(t *testing.T, keyHex string) *Key {
	t.Helper()

	key, err := Load("", keyHex)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	return key
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	key := mustKey(t, testKeyHex)
	aad := AAD("svc", "DATABASE_URL")

	tests := []struct {
		name      string
		plaintext string
	}{
		{name: "ascii", plaintext: "plain value with spaces"},
		{name: "empty", plaintext: ""},
		{name: "unicode", plaintext: "pässwörd ✓"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ciphertext, nonce, err := key.Encrypt([]byte(tt.plaintext), aad)
			if err != nil {
				t.Fatalf("Encrypt() error = %v, want nil", err)
			}

			if len(nonce) != nonceSize {
				t.Fatalf("nonce length = %d, want %d", len(nonce), nonceSize)
			}

			if tt.plaintext != "" && bytes.Contains(ciphertext, []byte(tt.plaintext)) {
				t.Fatal("ciphertext contains plaintext")
			}

			got, err := key.Decrypt(ciphertext, nonce, aad)
			if err != nil {
				t.Fatalf("Decrypt() error = %v, want nil", err)
			}

			if string(got) != tt.plaintext {
				t.Errorf("Decrypt() = %q, want %q", got, tt.plaintext)
			}
		})
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	t.Parallel()

	key := mustKey(t, testKeyHex)
	aad := AAD("svc", "K")

	first, nonce1, err := key.Encrypt([]byte("same"), aad)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	second, nonce2, err := key.Encrypt([]byte("same"), aad)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	if bytes.Equal(nonce1, nonce2) || bytes.Equal(first, second) {
		t.Error("two encryptions of the same value produced identical output")
	}
}

func TestDecryptFailures(t *testing.T) {
	t.Parallel()

	key := mustKey(t, testKeyHex)
	aad := AAD("svc", "K")

	ciphertext, nonce, err := key.Encrypt([]byte("value"), aad)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tampered := append([]byte{}, ciphertext...)
	tampered[0] ^= 0xff

	tests := []struct {
		name       string
		key        *Key
		ciphertext []byte
		nonce      []byte
		aad        []byte
	}{
		{name: "tampered ciphertext", key: key, ciphertext: tampered, nonce: nonce, aad: aad},
		{name: "wrong key", key: mustKey(t, otherKeyHex), ciphertext: ciphertext, nonce: nonce, aad: aad},
		{name: "aad mismatch", key: key, ciphertext: ciphertext, nonce: nonce, aad: AAD("svc", "OTHER")},
		{name: "short nonce", key: key, ciphertext: ciphertext, nonce: nonce[:4], aad: aad},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.key.Decrypt(tt.ciphertext, tt.nonce, tt.aad); !errors.Is(err, ErrDecrypt) {
				t.Errorf("Decrypt() error = %v, want ErrDecrypt", err)
			}
		})
	}
}

func TestKeyID(t *testing.T) {
	t.Parallel()

	key := mustKey(t, testKeyHex)
	if len(key.ID()) != keyIDLen {
		t.Fatalf("ID() = %q, want %d hex chars", key.ID(), keyIDLen)
	}

	if key.ID() == mustKey(t, otherKeyHex).ID() {
		t.Error("different keys share an id")
	}

	if key.ID() != mustKey(t, testKeyHex).ID() {
		t.Error("same key yields different ids")
	}
}

func TestLoadValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keyHex  string
		wantErr error
	}{
		{name: "missing", keyHex: "", wantErr: ErrKeyMissing},
		{name: "not hex", keyHex: "zz", wantErr: ErrKeyInvalid},
		{name: "wrong length", keyHex: "0011", wantErr: ErrKeyInvalid},
		{name: "valid", keyHex: testKeyHex, wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load("", tt.keyHex)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Load() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	raw, err := hex.DecodeString(testKeyHex)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		content []byte
		mode    os.FileMode
		wantErr error
	}{
		{name: "raw bytes 0600", content: raw, mode: 0o600, wantErr: nil},
		{name: "hex with newline 0600", content: []byte(testKeyHex + "\n"), mode: 0o600, wantErr: nil},
		{name: "group readable refused", content: raw, mode: 0o640, wantErr: ErrKeyFileMode},
		{name: "world readable refused", content: raw, mode: 0o644, wantErr: ErrKeyFileMode},
		{name: "short content", content: []byte("short"), mode: 0o600, wantErr: ErrKeyInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "env.key")
			if err := os.WriteFile(path, tt.content, tt.mode); err != nil {
				t.Fatal(err)
			}

			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatal(err)
			}

			key, err := Load(path, "ignored-when-file-set")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Load() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr == nil && key.ID() != mustKey(t, testKeyHex).ID() {
				t.Errorf("ID() = %q, want the hex key id", key.ID())
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		_, err := Load(filepath.Join(t.TempDir(), "nope"), "")
		if !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("Load() error = %v, want ErrKeyInvalid", err)
		}
	})
}

func TestZero(t *testing.T) {
	t.Parallel()

	data := []byte("secret")
	Zero(data)

	if strings.TrimRight(string(data), "\x00") != "" {
		t.Errorf("Zero() left %q", data)
	}
}

func TestAADBindsServiceAndKey(t *testing.T) {
	t.Parallel()

	if bytes.Equal(AAD("a", "bc"), AAD("ab", "c")) {
		t.Error("AAD collides across service/key boundaries")
	}
}
