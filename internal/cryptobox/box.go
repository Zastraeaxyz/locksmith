// Package cryptobox encrypts small secrets at rest with AES-256-GCM.
//
// The key can come from an environment variable or from a 0600 key file. The
// sealed value carries its nonce, so callers only need to store one blob.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// KeySize is the AES-256 key length.
	KeySize = 32

	// nonceSize is the standard GCM nonce length.
	nonceSize = 12
)

// Box seals and opens secrets with one key.
type Box struct {
	aead cipher.AEAD
}

// New builds a box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("cryptobox: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: creating cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: creating GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// ParseKey accepts a 32-byte key as base64 or hex. Both padded and raw base64
// are accepted so a value copied from any common tool works.
func ParseKey(raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, errors.New("cryptobox: key is empty")
	}

	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == KeySize {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("cryptobox: key must be %d bytes encoded as base64 or hex", KeySize)
}

// LoadOrCreate builds a box from envKey when set, or from a key file at path.
// A missing key file is generated with 0600 permissions. The key is never
// returned to callers, so it cannot be logged accidentally.
func LoadOrCreate(envKey, path string) (*Box, error) {
	key, err := loadOrCreateKey(envKey, path)
	if err != nil {
		return nil, err
	}
	return New(key)
}

func loadOrCreateKey(envKey, path string) ([]byte, error) {
	if value := strings.TrimSpace(envKey); value != "" {
		return ParseKey(value)
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("cryptobox: key path is empty")
	}

	key, err := os.ReadFile(path)
	if err == nil {
		decoded, decodeErr := ParseKey(string(key))
		if decodeErr != nil {
			return nil, fmt.Errorf("cryptobox: reading key file %q: %w", path, decodeErr)
		}
		return decoded, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cryptobox: reading key file %q: %w", path, err)
	}

	key = make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("cryptobox: generating key: %w", err)
	}
	existing, err := writeKeyFile(path, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	return key, nil
}

// writeKeyFile stores key and returns the key already on disk when another
// process won the O_EXCL race.
func writeKeyFile(path string, key []byte) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("cryptobox: creating key directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf("cryptobox: reading key file %q: %w", path, readErr)
			}
			decoded, decodeErr := ParseKey(string(data))
			if decodeErr != nil {
				return nil, fmt.Errorf("cryptobox: reading key file %q: %w", path, decodeErr)
			}
			return decoded, nil
		}
		return nil, fmt.Errorf("cryptobox: creating key file %q: %w", path, err)
	}
	defer file.Close()

	encoded := base64.RawStdEncoding.EncodeToString(key)
	if _, err := file.WriteString(encoded + "\n"); err != nil {
		return nil, fmt.Errorf("cryptobox: writing key file %q: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("cryptobox: syncing key file %q: %w", path, err)
	}
	return nil, nil
}

// Seal encrypts plaintext and returns nonce+ciphertext.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("cryptobox: box is not configured")
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cryptobox: generating nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("cryptobox: box is not configured")
	}
	if len(sealed) < nonceSize {
		return nil, errors.New("cryptobox: sealed value is too short")
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: opening sealed value: %w", err)
	}
	return plaintext, nil
}
