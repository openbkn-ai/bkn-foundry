// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

type aesGCMCipher struct {
	aead cipher.AEAD
}

// NewAESGCMCipher creates an AES-GCM cipher using a 16, 24 or 32-byte key.
// Each Encrypt call generates a random 12-byte nonce and returns
// Base64(nonce || ciphertext || authentication tag). Decrypt verifies that format.
// A cipher can be shared by concurrent callers. Rotate the key before encrypting
// 2^32 messages with it. Signature is unsupported and returns an error.
func NewAESGCMCipher(key []byte) (Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &aesGCMCipher{aead: aead}, nil
}

// Encrypt authenticates and encrypts plaintext, including empty strings.
func (ci *aesGCMCipher) Encrypt(plaintext string) (string, error) {
	sealed := ci.aead.Seal(nil, nil, []byte(plaintext), nil) //nolint:gosec // G407: NewGCMWithRandomNonce generates the nonce internally and requires an empty nonce argument.
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt rejects malformed or unauthenticated ciphertext without returning plaintext.
func (ci *aesGCMCipher) Decrypt(encoded string) (string, error) {
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(payload) < ci.aead.Overhead() {
		return "", errors.New("invalid AES-GCM ciphertext")
	}
	plaintext, err := ci.aead.Open(nil, nil, payload, nil)
	if err != nil {
		return "", errors.New("invalid AES-GCM ciphertext")
	}
	return string(plaintext), nil
}

// Signature is not supported by AES-GCM.
func (ci *aesGCMCipher) Signature(string) (string, error) {
	return "", errors.New("AES-GCM does not support signatures")
}
