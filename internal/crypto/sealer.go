// Package crypto seals secrets (Notion tokens) for transport through the queue.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// Sealer encrypts and decrypts short strings with AES-256-GCM.
type Sealer struct{ aead cipher.AEAD }

// NewSealer builds a Sealer from a base64 (std encoding) 32-byte key.
func NewSealer(keyB64 string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("sealer key: invalid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("sealer key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal returns base64(nonce|ciphertext) using a fresh random 12-byte nonce.
func (s *Sealer) Seal(plain string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal; it fails on malformed or tampered input.
func (s *Sealer) Open(enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("sealed value: invalid base64: %w", err)
	}
	n := s.aead.NonceSize()
	if len(raw) < n+s.aead.Overhead() {
		return "", errors.New("sealed value too short")
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("sealed value failed authentication")
	}
	return string(plain), nil
}
