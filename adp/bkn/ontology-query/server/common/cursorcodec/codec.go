// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package cursorcodec encrypts server-owned opaque cursor payloads.
package cursorcodec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Codec struct {
	aead cipher.AEAD
}

// New derives an AES-GCM key from key. An empty key creates a process-local
// random key, which deliberately invalidates cursors after a restart.
func New(key []byte) (*Codec, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("create cursor key: %w", err)
		}
	}
	derived := sha256.Sum256(key)
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, fmt.Errorf("create cursor cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create cursor AEAD: %w", err)
	}
	return &Codec{aead: aead}, nil
}

func NewFromEnvironment(name string) (*Codec, error) {
	return New([]byte(os.Getenv(name)))
}

func (codec *Codec) Encode(version string, payload any) (string, error) {
	if codec == nil || codec.aead == nil || version == "" {
		return "", fmt.Errorf("cursor codec is unavailable")
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode cursor payload: %w", err)
	}
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create cursor nonce: %w", err)
	}
	ciphertext := codec.aead.Seal(nil, nonce, plaintext, []byte(version))
	token := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (codec *Codec) Decode(version, token string, target any) error {
	if codec == nil || codec.aead == nil || version == "" || token == "" || target == nil {
		return fmt.Errorf("cursor codec is unavailable")
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(tokenBytes) != token || len(tokenBytes) <= codec.aead.NonceSize() {
		return fmt.Errorf("cursor is invalid")
	}
	nonce := tokenBytes[:codec.aead.NonceSize()]
	plaintext, err := codec.aead.Open(nil, nonce, tokenBytes[codec.aead.NonceSize():], []byte(version))
	if err != nil {
		return fmt.Errorf("cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("cursor is invalid")
	}
	return nil
}
