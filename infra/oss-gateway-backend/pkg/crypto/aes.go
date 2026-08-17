package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type AESCrypto struct {
	key []byte
}

func NewAESCrypto(key string) (*AESCrypto, error) {
	keyBytes := []byte(key)
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("AES key must be 32 bytes for AES-256")
	}
	return &AESCrypto{key: keyBytes}, nil
}

func (c *AESCrypto) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}

	plaintextBytes := []byte(plaintext)
	ciphertext := make([]byte, aes.BlockSize+len(plaintextBytes))
	iv := ciphertext[:aes.BlockSize]

	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	// CFB is unauthenticated and deprecated as of Go 1.24. Existing credentials
	// use this format, so switching directly to GCM would make them unreadable.
	// See #766 for the versioned migration plan.
	stream := cipher.NewCFBEncrypter(block, iv) //nolint:staticcheck // SA1019: migrate to versioned GCM in #766
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintextBytes)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (c *AESCrypto) Decrypt(ciphertext string) (string, error) {
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}

	if len(ciphertextBytes) < aes.BlockSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	iv := ciphertextBytes[:aes.BlockSize]
	ciphertextBytes = ciphertextBytes[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv) //nolint:staticcheck // SA1019: same compatibility constraint; see #766
	stream.XORKeyStream(ciphertextBytes, ciphertextBytes)

	return string(ciphertextBytes), nil
}
