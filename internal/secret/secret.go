// Package secret encrypts env values at rest with AES-256-GCM under one
// operator-provided key. Plaintext only ever lives in request scope.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Environment variables that provide the key.
const (
	KeyFileEnvVar = "TOUCHGRASS_ENV_KEY_FILE"
	KeyEnvVar     = "TOUCHGRASS_ENV_KEY"
)

const (
	// KeySize is the AES-256 key length in bytes.
	KeySize = 32
	// nonceSize is the GCM nonce length in bytes.
	nonceSize = 12
	// keyIDLen is the hex length of a key id.
	keyIDLen = 8
	// keyFileMode is the only accepted key file permission set.
	keyFileMode = fs.FileMode(0o600)
)

// Errors returned while loading or using a key.
var (
	ErrKeyMissing  = errors.New("secret: no env key configured")
	ErrKeyInvalid  = errors.New("secret: env key is invalid")
	ErrKeyFileMode = errors.New("secret: env key file must be mode 0600")
	ErrDecrypt     = errors.New("secret: decryption failed")
)

// Key is one loaded AES-256 key.
type Key struct {
	aead cipher.AEAD
	id   string
}

// Load reads the key from keyFile (32 raw or 64 hex bytes, mode 0600) or,
// when keyFile is empty, from the hex-encoded keyHex. Both empty returns
// ErrKeyMissing so callers can run with the pillar disabled.
func Load(keyFile, keyHex string) (*Key, error) {
	switch {
	case keyFile != "":
		return loadFile(keyFile)
	case keyHex != "":
		raw, err := hex.DecodeString(strings.TrimSpace(keyHex))
		if err != nil {
			return nil, fmt.Errorf("%w: %s is not hex", ErrKeyInvalid, KeyEnvVar)
		}

		return New(raw)
	default:
		return nil, ErrKeyMissing
	}
}

// loadFile reads and validates a key file.
func loadFile(path string) (*Key, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: reading key file: %w", ErrKeyInvalid, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: key file is not a regular file", ErrKeyInvalid)
	}

	if info.Mode().Perm() != keyFileMode {
		return nil, fmt.Errorf("%w: got %04o", ErrKeyFileMode, info.Mode().Perm())
	}

	data, err := os.ReadFile(path) //nolint:gosec // operator-configured key path, mode-checked above.
	if err != nil {
		return nil, fmt.Errorf("%w: reading key file: %w", ErrKeyInvalid, err)
	}

	defer Zero(data)

	return Parse(data)
}

// Parse accepts 32 raw bytes or 64 hex characters (surrounding whitespace
// ignored) and returns the key.
func Parse(data []byte) (*Key, error) {
	if len(data) == KeySize {
		return New(data)
	}

	trimmed := strings.TrimSpace(string(data))

	raw, err := hex.DecodeString(trimmed)
	if err != nil || len(raw) != KeySize {
		return nil, fmt.Errorf("%w: want %d raw bytes or %d hex characters", ErrKeyInvalid, KeySize, KeySize*2)
	}

	return New(raw)
}

// New builds a Key from 32 raw bytes. The caller may zero raw afterwards;
// the cipher keeps its own schedule.
func New(raw []byte) (*Key, error) {
	if len(raw) != KeySize {
		return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrKeyInvalid, KeySize, len(raw))
	}

	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyInvalid, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyInvalid, err)
	}

	sum := sha256.Sum256(raw)

	return &Key{aead: aead, id: hex.EncodeToString(sum[:])[:keyIDLen]}, nil
}

// ID is the first eight hex characters of SHA-256(key); it tags stored
// values so a future rotation can tell them apart.
func (k *Key) ID() string {
	return k.id
}

// Encrypt seals plaintext under a fresh random nonce bound to aad.
func (k *Key) Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, nonceSize)

	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generating nonce: %w", err)
	}

	return k.aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

// Decrypt opens ciphertext; any tamper, wrong key, or AAD mismatch returns
// ErrDecrypt without detail.
func (k *Key) Decrypt(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, ErrDecrypt
	}

	plaintext, err := k.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}

	return plaintext, nil
}

// AAD binds a value to its service and key name so a ciphertext moved to
// another row fails to open.
func AAD(serviceID, key string) []byte {
	return []byte(serviceID + "\x00" + key)
}

// Zero overwrites a byte slice that held plaintext.
func Zero(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
